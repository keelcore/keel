//go:build !no_oidc && !no_authn

package mw

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/audit"
	"github.com/keelcore/keel/pkg/core/ctxkeys"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/tlspolicy"
)

const (
	oidcSessionCookie = "keel_session"
	oidcStateCookie   = "keel_oidc_state"
	oidcLogoutPath    = "/oauth2/logout"
	oidcUserHeader    = "X-Auth-Request-User"
	oidcEmailHeader   = "X-Auth-Request-Email"

	oidcStateTTL       = 10 * time.Minute
	oidcDiscoveryTTL   = time.Hour
	oidcMinKeyBytes    = 32
	oidcMaxResponse    = 1 << 20
	oidcMaxCookieBytes = 4000
)

// oidcMethods are the ID token algorithms accepted: asymmetric only, so a token is checked
// against the IdP's published keys and never against a shared secret.
var oidcMethods = []string{"RS256", "RS384", "RS512", "PS256", "PS384", "PS512", "ES256", "ES384", "ES512"}

type oidcDiscovery struct {
	Issuer       string `json:"issuer"`
	AuthEndpoint string `json:"authorization_endpoint"`
	TokenURL     string `json:"token_endpoint"`
	JWKSURI      string `json:"jwks_uri"`
	EndSession   string `json:"end_session_endpoint"`
}

// oidcState is the sealed state cookie: what the callback needs to finish one login.
type oidcState struct {
	State    string
	Nonce    string
	Verifier string
	Return   string
	Exp      int64
}

// oidcSession is the sealed session cookie. Exp is when the ID token runs out and refresh is due;
// Absolute is when the session ends regardless (session_ttl).
type oidcSession struct {
	Sub      string
	Email    string
	Exp      int64
	Absolute int64
	Refresh  string
}

type oidcHandler struct {
	cfg          config.OIDCConfig
	authed       http.Handler
	bearer       http.Handler
	log          *logging.Logger
	record       func(audit.Event)
	aead         cipher.AEAD
	client       *http.Client
	jwks         *jwksCache
	callbackPath string
	secure       bool

	mu        sync.Mutex
	disc      *oidcDiscovery
	discUntil time.Time
}

// OIDC is the OpenID Connect authorization-code flow with PKCE for browsers. A valid session
// reaches authed with the identity in X-Auth-Request-User and X-Auth-Request-Email (client copies
// are always removed) and the session cookie hidden from the upstream. A browser navigation with
// no session is sent to the IdP. Anything else, including a request carrying a token, goes to
// bearer (the JWT path), or gets 401 when bearer is nil. record (optional) receives login, refresh
// and logout events.
func OIDC(cfg config.OIDCConfig, authed, bearer http.Handler, log *logging.Logger, record func(audit.Event)) (http.Handler, error) {
	redirect, err := url.Parse(cfg.RedirectURL)
	if err != nil {
		return nil, fmt.Errorf("oidc: redirect_url: %w", err)
	}
	aead, err := oidcCookieCipher(cfg.CookieKeyFile)
	if err != nil {
		return nil, err
	}
	client, err := oidcClient(cfg.CAFile)
	if err != nil {
		return nil, err
	}
	if record == nil {
		record = func(audit.Event) {}
	}
	jwks := newJWKSCache()
	jwks.client = client
	return &oidcHandler{
		cfg: cfg, authed: authed, bearer: bearer, log: log, record: record, aead: aead, client: client, jwks: jwks,
		callbackPath: redirect.Path, secure: redirect.Scheme == "https",
	}, nil
}

// oidcCookieCipher derives the cookie key from the key file. NewGCMWithRandomNonce is the form
// FIPS 140-only mode permits.
func oidcCookieCipher(path string) (cipher.AEAD, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("oidc: cookie_key_file: %w", err)
	}
	if len(strings.TrimSpace(string(b))) < oidcMinKeyBytes {
		return nil, fmt.Errorf("oidc: cookie_key_file must hold at least %d bytes", oidcMinKeyBytes)
	}
	key := sha256.Sum256(b)
	block, _ := aes.NewCipher(key[:])              // a 32-byte key: cannot fail
	aead, _ := cipher.NewGCMWithRandomNonce(block) // an AES block: cannot fail
	return aead, nil
}

// oidcClient is the outbound client for the IdP, on the TLS policy; caFile adds a private CA.
func oidcClient(caFile string) (*http.Client, error) {
	c := tlspolicy.NewHTTPClient(10 * time.Second)
	c.CheckRedirect = noFollow
	if caFile == "" {
		return c, nil
	}
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("oidc: ca_file: %w", err)
	}
	pool, err := oidcSystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("oidc: ca_file %s holds no PEM certificate", caFile)
	}
	c.Transport.(*http.Transport).TLSClientConfig.RootCAs = pool
	return c, nil
}

// oidcSystemCertPool is a seam over x509.SystemCertPool so tests can inject a failure.
var oidcSystemCertPool = x509.SystemCertPool

// noFollow keeps IdP requests on the endpoints discovery named.
func noFollow(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func (o *oidcHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.Header.Del(oidcUserHeader)
	r.Header.Del(oidcEmailHeader)
	switch r.URL.Path {
	case o.callbackPath:
		o.callback(w, r)
		return
	case oidcLogoutPath:
		o.logout(w, r)
		return
	}
	if r.Header.Get("Authorization") != "" {
		o.toBearer(w, r)
		return
	}
	if s, ok := o.currentSession(w, r); ok {
		o.forward(w, r, s)
		return
	}
	if (r.Method == http.MethodGet || r.Method == http.MethodHead) && strings.Contains(r.Header.Get("Accept"), "text/html") {
		o.startLogin(w, r)
		return
	}
	o.toBearer(w, r)
}

func (o *oidcHandler) toBearer(w http.ResponseWriter, r *http.Request) {
	if o.bearer != nil {
		o.bearer.ServeHTTP(w, r)
		return
	}
	http.Error(w, "authentication required", http.StatusUnauthorized)
}

// currentSession reads the session cookie, refreshing it when the ID token has run out.
func (o *oidcHandler) currentSession(w http.ResponseWriter, r *http.Request) (oidcSession, bool) {
	var s oidcSession
	if !o.readCookie(r, oidcSessionCookie, &s) {
		return s, false
	}
	now := time.Now().Unix()
	if now >= s.Absolute {
		return s, false
	}
	if now < s.Exp {
		return s, true
	}
	if s.Refresh == "" {
		return s, false
	}
	renewed, err := o.refresh(r.Context(), s)
	if err != nil {
		o.log.Warn("oidc_refresh_failed", map[string]any{"err": err.Error()})
		o.record(audit.Event{Event: "oidc_refresh", Principal: s.Sub, SourceIP: clientIP(r), Outcome: "fail"})
		return s, false
	}
	o.record(audit.Event{Event: "oidc_refresh", Principal: renewed.Sub, SourceIP: clientIP(r), Outcome: "ok"})
	o.setSession(w, renewed)
	return renewed, true
}

func (o *oidcHandler) forward(w http.ResponseWriter, r *http.Request, s oidcSession) {
	r.Header.Set(oidcUserHeader, s.Sub)
	if s.Email != "" {
		r.Header.Set(oidcEmailHeader, s.Email)
	}
	hideCookies(r)
	o.authed.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxkeys.Principal, s.Sub)))
}

// hideCookies removes keel's own cookies from what the upstream receives.
func hideCookies(r *http.Request) {
	cookies := r.Cookies()
	r.Header.Del("Cookie")
	for _, c := range cookies {
		if c.Name != oidcSessionCookie && c.Name != oidcStateCookie {
			r.AddCookie(c)
		}
	}
}

func (o *oidcHandler) startLogin(w http.ResponseWriter, r *http.Request) {
	disc, err := o.discovery(r.Context())
	if err != nil {
		o.log.Warn("oidc_discovery_failed", map[string]any{"err": err.Error()})
		http.Error(w, "identity provider unavailable", http.StatusBadGateway)
		return
	}
	st := oidcState{
		State: randomToken(), Nonce: randomToken(), Verifier: randomToken(),
		Return: localPath(r.URL.RequestURI()), Exp: time.Now().Add(oidcStateTTL).Unix(),
	}
	http.SetCookie(w, o.cookie(oidcStateCookie, o.seal(oidcStateCookie, st), o.callbackPath, int(oidcStateTTL.Seconds())))
	challenge := sha256.Sum256([]byte(st.Verifier))
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {o.cfg.ClientID},
		"redirect_uri":          {o.cfg.RedirectURL},
		"scope":                 {strings.Join(o.cfg.Scopes, " ")},
		"state":                 {st.State},
		"nonce":                 {st.Nonce},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"},
	}
	sep := "?"
	if strings.Contains(disc.AuthEndpoint, "?") {
		sep = "&"
	}
	http.Redirect(w, r, disc.AuthEndpoint+sep+q.Encode(), http.StatusFound)
}

func (o *oidcHandler) callback(w http.ResponseWriter, r *http.Request) {
	var st oidcState
	ok := o.readCookie(r, oidcStateCookie, &st)
	http.SetCookie(w, o.cookie(oidcStateCookie, "", o.callbackPath, -1)) // single use
	q := r.URL.Query()
	switch {
	case !ok || time.Now().Unix() >= st.Exp || subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(st.State)) != 1:
		o.loginFailed(w, r, "state does not match")
	case q.Get("error") != "":
		o.loginFailed(w, r, "identity provider: "+q.Get("error"))
	case q.Get("code") == "":
		o.loginFailed(w, r, "no code")
	default:
		o.finishLogin(w, r, q.Get("code"), st)
	}
}

func (o *oidcHandler) finishLogin(w http.ResponseWriter, r *http.Request, code string, st oidcState) {
	tok, err := o.tokenRequest(r.Context(), url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {o.cfg.RedirectURL}, "code_verifier": {st.Verifier},
	})
	if err != nil {
		o.loginFailed(w, r, "token endpoint: "+err.Error())
		return
	}
	claims, err := o.verifyIDToken(r.Context(), tok.IDToken)
	if err != nil {
		o.loginFailed(w, r, "id_token: "+err.Error())
		return
	}
	nonce, _ := claims["nonce"].(string)
	if subtle.ConstantTimeCompare([]byte(nonce), []byte(st.Nonce)) != 1 {
		o.loginFailed(w, r, "nonce does not match")
		return
	}
	s, err := sessionFrom(claims, tok.RefreshToken, time.Now().Add(o.cfg.SessionTTL.Duration).Unix())
	if err != nil {
		o.loginFailed(w, r, "id_token: "+err.Error())
		return
	}
	o.setSession(w, s)
	o.record(audit.Event{Event: "oidc_login", Principal: s.Sub, SourceIP: clientIP(r), Outcome: "allow"})
	http.Redirect(w, r, localPath(st.Return), http.StatusFound)
}

func (o *oidcHandler) loginFailed(w http.ResponseWriter, r *http.Request, detail string) {
	o.log.Warn("oidc_login_failed", map[string]any{"reason": detail})
	o.record(audit.Event{Event: "oidc_login_failed", SourceIP: clientIP(r), Outcome: "deny", Detail: detail})
	http.Error(w, "login failed", http.StatusUnauthorized)
}

func (o *oidcHandler) logout(w http.ResponseWriter, r *http.Request) {
	var s oidcSession
	if o.readCookie(r, oidcSessionCookie, &s) {
		o.record(audit.Event{Event: "oidc_logout", Principal: s.Sub, SourceIP: clientIP(r), Outcome: "ok"})
	}
	http.SetCookie(w, o.cookie(oidcSessionCookie, "", "/", -1))
	target := "/"
	if disc, err := o.discovery(r.Context()); err == nil && disc.EndSession != "" {
		target = disc.EndSession
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func (o *oidcHandler) refresh(ctx context.Context, s oidcSession) (oidcSession, error) {
	tok, err := o.tokenRequest(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {s.Refresh}})
	if err != nil {
		return s, err
	}
	claims, err := o.verifyIDToken(ctx, tok.IDToken)
	if err != nil {
		return s, err
	}
	renewed, err := sessionFrom(claims, tok.RefreshToken, s.Absolute)
	if err != nil {
		return s, err
	}
	if renewed.Sub != s.Sub {
		return s, errors.New("refresh returned another subject")
	}
	if renewed.Refresh == "" {
		renewed.Refresh = s.Refresh // the IdP did not rotate it
	}
	return renewed, nil
}

func sessionFrom(claims jwt.MapClaims, refresh string, absolute int64) (oidcSession, error) {
	sub, _ := claims["sub"].(string)
	if sub == "" {
		return oidcSession{}, errors.New("no sub claim")
	}
	exp, _ := claims.GetExpirationTime() // the parser required a valid exp
	email, _ := claims["email"].(string)
	return oidcSession{Sub: sub, Email: email, Exp: exp.Unix(), Absolute: absolute, Refresh: refresh}, nil
}

// setSession writes the session cookie. A refresh token that would overflow the cookie limit is
// dropped, so the user logs in again when the ID token runs out.
func (o *oidcHandler) setSession(w http.ResponseWriter, s oidcSession) {
	sealed := o.seal(oidcSessionCookie, s)
	if len(sealed) > oidcMaxCookieBytes {
		s.Refresh = ""
		sealed = o.seal(oidcSessionCookie, s)
	}
	http.SetCookie(w, o.cookie(oidcSessionCookie, sealed, "/", int(s.Absolute-time.Now().Unix())))
}

func (o *oidcHandler) cookie(name, value, path string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name: name, Value: value, Path: path, MaxAge: maxAge,
		HttpOnly: true, Secure: o.secure, SameSite: http.SameSiteLaxMode,
	}
}

// seal encrypts v, bound to the cookie's name, so one cookie's value cannot stand in for another's.
func (o *oidcHandler) seal(name string, v any) string {
	b, _ := json.Marshal(v) // strings and numbers: cannot fail
	return base64.RawURLEncoding.EncodeToString(o.aead.Seal(nil, nil, b, []byte(name)))
}

func (o *oidcHandler) readCookie(r *http.Request, name string, v any) bool {
	c, err := r.Cookie(name)
	if err != nil {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		return false
	}
	b, err := o.aead.Open(nil, nil, raw, []byte(name))
	if err != nil {
		return false
	}
	return json.Unmarshal(b, v) == nil
}

type oidcTokens struct {
	IDToken      string `json:"id_token"`
	RefreshToken string `json:"refresh_token"`
}

func (o *oidcHandler) tokenRequest(ctx context.Context, form url.Values) (oidcTokens, error) {
	var tok oidcTokens
	disc, err := o.discovery(ctx)
	if err != nil {
		return tok, err
	}
	form.Set("client_id", o.cfg.ClientID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, disc.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return tok, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if o.cfg.ClientSecret != "" {
		req.SetBasicAuth(url.QueryEscape(o.cfg.ClientID), url.QueryEscape(o.cfg.ClientSecret))
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return tok, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return tok, fmt.Errorf("status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, oidcMaxResponse)).Decode(&tok); err != nil {
		return tok, err
	}
	return tok, nil
}

func (o *oidcHandler) verifyIDToken(ctx context.Context, raw string) (jwt.MapClaims, error) {
	disc, err := o.discovery(ctx)
	if err != nil {
		return nil, err
	}
	claims := jwt.MapClaims{}
	_, err = jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		return o.jwks.keysFor(disc.JWKSURI, t)
	},
		jwt.WithValidMethods(oidcMethods), jwt.WithExpirationRequired(), jwt.WithLeeway(jwtClockLeeway),
		jwt.WithIssuer(o.cfg.Issuer), jwt.WithAudience(o.cfg.ClientID))
	if err != nil {
		return nil, err
	}
	return claims, nil
}

// discovery fetches and caches the provider's metadata. Its issuer must be the configured one, and
// every endpoint keel calls must be https (or loopback).
func (o *oidcHandler) discovery(ctx context.Context) (*oidcDiscovery, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.disc != nil && time.Now().Before(o.discUntil) {
		return o.disc, nil
	}
	target := strings.TrimRight(o.cfg.Issuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discovery status %d", resp.StatusCode)
	}
	var d oidcDiscovery
	if err := json.NewDecoder(io.LimitReader(resp.Body, oidcMaxResponse)).Decode(&d); err != nil {
		return nil, fmt.Errorf("discovery: %w", err)
	}
	if d.Issuer != o.cfg.Issuer {
		return nil, fmt.Errorf("discovery issuer %q is not the configured %q", d.Issuer, o.cfg.Issuer)
	}
	for _, u := range []string{d.TokenURL, d.JWKSURI} {
		if err := requireSecureJWKSURL(u); err != nil {
			return nil, fmt.Errorf("discovery: %w", err)
		}
	}
	if d.AuthEndpoint == "" {
		return nil, errors.New("discovery: no authorization_endpoint")
	}
	o.disc, o.discUntil = &d, time.Now().Add(oidcDiscoveryTTL)
	return o.disc, nil
}

func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// localPath keeps a return target on this site: a path, never another host.
func localPath(p string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.HasPrefix(p, "/\\") {
		return "/"
	}
	return p
}
