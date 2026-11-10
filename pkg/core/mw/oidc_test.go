//go:build !no_oidc && !no_authn

package mw

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/audit"
	"github.com/keelcore/keel/pkg/core/ctxkeys"
	"github.com/keelcore/keel/pkg/core/logging"
)

// fakeOP is an in-process OpenID Provider: discovery, JWKS and a token endpoint that checks the
// PKCE verifier. The real-IdP flow is tests/compose (dex).
type fakeOP struct {
	t   *testing.T
	srv *httptest.Server
	key *ecdsa.PrivateKey

	mu         sync.Mutex
	challenge  string
	nonce      string
	sub        string
	ttl        time.Duration
	refresh    string
	tokenCalls int
	failToken  bool
	badAud     bool
	badNonce   bool
	noEndSess  bool
	wrongIss   bool
	lastGrant  url.Values
}

func newFakeOP(t *testing.T) *fakeOP {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	op := &fakeOP{t: t, key: key, sub: "alice", ttl: time.Hour}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", op.discovery)
	mux.HandleFunc("/jwks", op.jwks)
	mux.HandleFunc("/token", op.token)
	op.srv = httptest.NewServer(mux)
	t.Cleanup(op.srv.Close)
	return op
}

func (op *fakeOP) discovery(w http.ResponseWriter, _ *http.Request) {
	op.mu.Lock()
	defer op.mu.Unlock()
	iss := op.srv.URL
	if op.wrongIss {
		iss = "https://other.example"
	}
	doc := map[string]string{
		"issuer": iss, "authorization_endpoint": op.srv.URL + "/auth",
		"token_endpoint": op.srv.URL + "/token", "jwks_uri": op.srv.URL + "/jwks",
	}
	if !op.noEndSess {
		doc["end_session_endpoint"] = op.srv.URL + "/logout"
	}
	_ = json.NewEncoder(w).Encode(doc)
}

func b64(i *big.Int) string {
	return base64.RawURLEncoding.EncodeToString(i.FillBytes(make([]byte, 32)))
}

func (op *fakeOP) jwks(w http.ResponseWriter, _ *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
		"kty": "EC", "crv": "P-256", "kid": "k1", "alg": "ES256", "use": "sig", "x": b64(op.key.X), "y": b64(op.key.Y),
	}}})
}

func (op *fakeOP) token(w http.ResponseWriter, r *http.Request) {
	op.mu.Lock()
	defer op.mu.Unlock()
	op.tokenCalls++
	_ = r.ParseForm()
	op.lastGrant = r.PostForm
	if op.failToken {
		http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
		return
	}
	if r.PostForm.Get("grant_type") == "authorization_code" {
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if base64.RawURLEncoding.EncodeToString(sum[:]) != op.challenge {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
	}
	aud, nonce := "keel-client", op.nonce
	if op.badAud {
		aud = "someone-else"
	}
	if op.badNonce {
		nonce = "wrong"
	}
	claims := jwt.MapClaims{"iss": op.srv.URL, "sub": op.sub, "aud": aud, "email": op.sub + "@example.com",
		"exp": time.Now().Add(op.ttl).Unix(), "iat": time.Now().Unix()}
	if r.PostForm.Get("grant_type") == "authorization_code" {
		claims["nonce"] = nonce
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	tok.Header["kid"] = "k1"
	signed, err := tok.SignedString(op.key)
	if err != nil {
		op.t.Error(err)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"id_token": signed, "access_token": "at", "token_type": "Bearer", "refresh_token": op.refresh})
}

type oidcHarness struct {
	op      *fakeOP
	h       http.Handler
	events  *[]audit.Event
	authed  *http.Request
	bearer  *int
	cookies map[string]*http.Cookie
}

func newOIDC(t *testing.T, op *fakeOP, edit func(*config.OIDCConfig)) *oidcHarness {
	t.Helper()
	keyFile := filepath.Join(t.TempDir(), "cookie.key")
	if err := os.WriteFile(keyFile, []byte("a-cookie-key-with-plenty-of-entropy-in-it"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults().Authn.OIDC
	cfg.Enabled, cfg.Issuer, cfg.ClientID, cfg.ClientSecret = true, op.srv.URL, "keel-client", "shh"
	cfg.RedirectURL, cfg.CookieKeyFile = "http://localhost:8080/oauth2/callback", keyFile
	if edit != nil {
		edit(&cfg)
	}
	var events []audit.Event
	bearer := 0
	x := &oidcHarness{op: op, events: &events, bearer: &bearer, cookies: map[string]*http.Cookie{}}
	authed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { x.authed = r; w.WriteHeader(http.StatusTeapot) })
	bearerH := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { bearer++; w.WriteHeader(http.StatusUnauthorized) })
	h, err := OIDC(cfg, authed, bearerH, logging.New(logging.Config{Out: io.Discard}), func(e audit.Event) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}
	x.h = h
	return x
}

// do sends a request with the jar's cookies and keeps the ones it sets.
func (x *oidcHarness) do(method, target string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	r.RemoteAddr = "203.0.113.7:4321"
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	for _, c := range x.cookies {
		r.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	x.h.ServeHTTP(rec, r)
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge < 0 {
			delete(x.cookies, c.Name)
		} else {
			x.cookies[c.Name] = c
		}
	}
	return rec
}

var html = map[string]string{"Accept": "text/html,application/xhtml+xml"}

// login walks the redirect and callback, leaving a session in the jar.
func (x *oidcHarness) login(t *testing.T) {
	t.Helper()
	rec := x.do(http.MethodGet, "/app?x=1", html)
	if rec.Code != http.StatusFound {
		t.Fatalf("start: status %d", rec.Code)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	q := loc.Query()
	x.op.mu.Lock()
	x.op.challenge, x.op.nonce = q.Get("code_challenge"), q.Get("nonce")
	x.op.mu.Unlock()
	cb := x.do(http.MethodGet, "/oauth2/callback?code=c1&state="+url.QueryEscape(q.Get("state")), html)
	if cb.Code != http.StatusFound || cb.Header().Get("Location") != "/app?x=1" {
		t.Fatalf("callback: %d %q %s", cb.Code, cb.Header().Get("Location"), cb.Body.String())
	}
}

func TestOIDC_RedirectsABrowserWithPKCE(t *testing.T) {
	x := newOIDC(t, newFakeOP(t), nil)
	rec := x.do(http.MethodGet, "/app", html)
	if rec.Code != http.StatusFound {
		t.Fatalf("status %d", rec.Code)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	q := loc.Query()
	if !strings.HasSuffix(loc.Path, "/auth") || q.Get("response_type") != "code" || q.Get("client_id") != "keel-client" ||
		q.Get("redirect_uri") != "http://localhost:8080/oauth2/callback" || q.Get("code_challenge_method") != "S256" ||
		q.Get("code_challenge") == "" || q.Get("state") == "" || q.Get("nonce") == "" || !strings.Contains(q.Get("scope"), "openid") {
		t.Fatalf("authorize URL = %s", loc)
	}
	state := x.cookies[oidcStateCookie]
	if state == nil || !state.HttpOnly || state.SameSite != http.SameSiteLaxMode {
		t.Fatalf("state cookie = %+v", state)
	}
	if strings.Contains(state.Value, q.Get("state")) {
		t.Fatal("state cookie is readable; it must be sealed")
	}
}

func TestOIDC_NonBrowserWithoutSessionGoesToBearer(t *testing.T) {
	x := newOIDC(t, newFakeOP(t), nil)
	if rec := x.do(http.MethodGet, "/api", map[string]string{"Accept": "application/json"}); rec.Code != http.StatusUnauthorized || *x.bearer != 1 {
		t.Fatalf("status %d, bearer calls %d", rec.Code, *x.bearer)
	}
	if rec := x.do(http.MethodPost, "/form", html); rec.Code != http.StatusUnauthorized {
		t.Fatalf("POST: status %d: only a navigation redirects", rec.Code)
	}
	if rec := x.do(http.MethodGet, "/app", map[string]string{"Authorization": "Bearer t", "Accept": "text/html"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("Authorization: status %d: a token goes to the JWT path", rec.Code)
	}
}

func TestOIDC_WithoutABearerHandlerIs401(t *testing.T) {
	op := newFakeOP(t)
	keyFile := filepath.Join(t.TempDir(), "k")
	_ = os.WriteFile(keyFile, []byte("a-cookie-key-with-plenty-of-entropy-in-it"), 0o600)
	cfg := config.Defaults().Authn.OIDC
	cfg.Issuer, cfg.ClientID, cfg.RedirectURL, cfg.CookieKeyFile = op.srv.URL, "c", "http://localhost/cb", keyFile
	h, err := OIDC(cfg, http.NotFoundHandler(), nil, logging.New(logging.Config{Out: io.Discard}), nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestOIDC_LoginEstablishesASession(t *testing.T) {
	x := newOIDC(t, newFakeOP(t), nil)
	x.login(t)
	s := x.cookies[oidcSessionCookie]
	if s == nil || !s.HttpOnly || s.SameSite != http.SameSiteLaxMode || strings.Contains(s.Value, "alice") {
		t.Fatalf("session cookie = %+v", s)
	}
	if x.cookies[oidcStateCookie] != nil {
		t.Fatal("state cookie must be cleared after the callback")
	}
	if got := x.op.lastGrant; got.Get("code") != "c1" || got.Get("code_verifier") == "" {
		t.Fatalf("grant = %v", got)
	}
	found := false
	for _, e := range *x.events {
		found = found || (e.Event == "oidc_login" && e.Principal == "alice" && e.Outcome == "allow" && e.SourceIP == "203.0.113.7")
	}
	if !found {
		t.Fatalf("events = %+v", *x.events)
	}
}

// A session forwards the identity, hides the cookie from the upstream, and replaces forged copies.
func TestOIDC_SessionForwardsIdentity(t *testing.T) {
	x := newOIDC(t, newFakeOP(t), nil)
	x.login(t)
	rec := x.do(http.MethodGet, "/app", map[string]string{"X-Auth-Request-User": "mallory", "X-Auth-Request-Email": "m@evil", "Cookie": "other=1"})
	if rec.Code != http.StatusTeapot {
		t.Fatalf("status %d", rec.Code)
	}
	r := x.authed
	if got := r.Header.Values("X-Auth-Request-User"); len(got) != 1 || got[0] != "alice" {
		t.Fatalf("user = %v", got)
	}
	if r.Header.Get("X-Auth-Request-Email") != "alice@example.com" {
		t.Fatalf("email = %q", r.Header.Get("X-Auth-Request-Email"))
	}
	if r.Context().Value(ctxkeys.Principal) != "alice" {
		t.Fatalf("principal = %v", r.Context().Value(ctxkeys.Principal))
	}
	if c := r.Header.Get("Cookie"); strings.Contains(c, oidcSessionCookie) || !strings.Contains(c, "other=1") {
		t.Fatalf("Cookie upstream = %q", c)
	}
}

// Without a session, forged identity headers never pass through.
func TestOIDC_ForgedIdentityHeadersAreStripped(t *testing.T) {
	var seen http.Header
	op := newFakeOP(t)
	keyFile := filepath.Join(t.TempDir(), "k")
	_ = os.WriteFile(keyFile, []byte("a-cookie-key-with-plenty-of-entropy-in-it"), 0o600)
	cfg := config.Defaults().Authn.OIDC
	cfg.Issuer, cfg.ClientID, cfg.RedirectURL, cfg.CookieKeyFile = op.srv.URL, "c", "http://localhost/cb", keyFile
	h, err := OIDC(cfg, http.NotFoundHandler(), http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = r.Header.Clone() }),
		logging.New(logging.Config{Out: io.Discard}), nil)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/api", nil)
	r.Header.Set("X-Auth-Request-User", "mallory")
	r.Header.Set("Authorization", "Bearer t")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if seen == nil || seen.Get("X-Auth-Request-User") != "" {
		t.Fatalf("headers = %v", seen)
	}
}

func TestOIDC_TamperedSessionRestartsLogin(t *testing.T) {
	x := newOIDC(t, newFakeOP(t), nil)
	x.login(t)
	c := x.cookies[oidcSessionCookie]
	first := "A"
	if c.Value[0] == 'A' { // the ciphertext is random: a fixed replacement sometimes changes nothing
		first = "B"
	}
	c.Value = first + c.Value[1:]
	if rec := x.do(http.MethodGet, "/app", html); rec.Code != http.StatusFound {
		t.Fatalf("status %d, want a new login", rec.Code)
	}
}

func TestOIDC_CallbackRejects(t *testing.T) {
	for name, tc := range map[string]struct {
		setup func(*fakeOP)
		query func(state string) string
		event string
	}{
		"wrong state": {nil, func(string) string { return "code=c&state=forged" }, "state"},
		"no code":     {nil, func(s string) string { return "state=" + url.QueryEscape(s) }, "code"},
		"idp error":   {nil, func(s string) string { return "error=access_denied&state=" + url.QueryEscape(s) }, "access_denied"},
		"token fails": {func(o *fakeOP) { o.failToken = true }, func(s string) string { return "code=c&state=" + url.QueryEscape(s) }, "token"},
		"wrong aud":   {func(o *fakeOP) { o.badAud = true }, func(s string) string { return "code=c&state=" + url.QueryEscape(s) }, "id_token"},
		"wrong nonce": {func(o *fakeOP) { o.badNonce = true }, func(s string) string { return "code=c&state=" + url.QueryEscape(s) }, "nonce"},
	} {
		x := newOIDC(t, newFakeOP(t), nil)
		if tc.setup != nil {
			tc.setup(x.op)
		}
		loc, _ := url.Parse(x.do(http.MethodGet, "/app", html).Header().Get("Location"))
		x.op.mu.Lock()
		x.op.challenge, x.op.nonce = loc.Query().Get("code_challenge"), loc.Query().Get("nonce")
		x.op.mu.Unlock()
		rec := x.do(http.MethodGet, "/oauth2/callback?"+tc.query(loc.Query().Get("state")), html)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, rec.Code)
		}
		if x.cookies[oidcSessionCookie] != nil {
			t.Errorf("%s: session issued", name)
		}
		failed := false
		for _, e := range *x.events {
			failed = failed || (e.Event == "oidc_login_failed" && e.Outcome == "deny" && strings.Contains(e.Detail, tc.event))
		}
		if !failed {
			t.Errorf("%s: events = %+v", name, *x.events)
		}
	}
}

func TestOIDC_CallbackWithoutStateCookie(t *testing.T) {
	x := newOIDC(t, newFakeOP(t), nil)
	if rec := x.do(http.MethodGet, "/oauth2/callback?code=c&state=s", html); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", rec.Code)
	}
}

// Only a local path survives as the return target: never another host.
func TestOIDC_ReturnPathIsLocal(t *testing.T) {
	for in, want := range map[string]string{
		"/app?x=1":         "/app?x=1",
		"//evil.example/x": "/",
		"/\\evil.example":  "/",
		"":                 "/",
	} {
		if got := localPath(in); got != want {
			t.Errorf("localPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOIDC_ExpiredSessionRefreshes(t *testing.T) {
	op := newFakeOP(t)
	op.refresh, op.ttl = "r1", -time.Minute+jwtClockLeeway+time.Second
	x := newOIDC(t, op, nil)
	op.ttl = 2 * time.Second
	x.login(t)
	time.Sleep(2100 * time.Millisecond)
	op.mu.Lock()
	op.ttl, op.sub = time.Hour, "alice"
	calls := op.tokenCalls
	op.mu.Unlock()
	rec := x.do(http.MethodGet, "/app", html)
	if rec.Code != http.StatusTeapot {
		t.Fatalf("status %d, want the session renewed", rec.Code)
	}
	if op.tokenCalls != calls+1 || op.lastGrant.Get("grant_type") != "refresh_token" || op.lastGrant.Get("refresh_token") != "r1" {
		t.Fatalf("calls %d→%d, grant %v", calls, op.tokenCalls, op.lastGrant)
	}
}

func TestOIDC_ExpiredSessionWithoutRefreshLogsInAgain(t *testing.T) {
	op := newFakeOP(t)
	x := newOIDC(t, op, nil)
	op.ttl = 2 * time.Second
	x.login(t)
	time.Sleep(2100 * time.Millisecond)
	if rec := x.do(http.MethodGet, "/app", html); rec.Code != http.StatusFound {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestOIDC_RefreshFailureLogsInAgain(t *testing.T) {
	op := newFakeOP(t)
	op.refresh = "r1"
	x := newOIDC(t, op, nil)
	op.ttl = 2 * time.Second
	x.login(t)
	time.Sleep(2100 * time.Millisecond)
	op.mu.Lock()
	op.failToken = true
	op.mu.Unlock()
	if rec := x.do(http.MethodGet, "/app", html); rec.Code != http.StatusFound {
		t.Fatalf("status %d", rec.Code)
	}
}

// session_ttl bounds a session absolutely, however long refresh keeps working.
func TestOIDC_SessionTTLIsAbsolute(t *testing.T) {
	op := newFakeOP(t)
	op.refresh = "r1"
	x := newOIDC(t, op, func(c *config.OIDCConfig) { c.SessionTTL = config.DurationOf(time.Second) })
	x.login(t)
	time.Sleep(1100 * time.Millisecond)
	if rec := x.do(http.MethodGet, "/app", html); rec.Code != http.StatusFound {
		t.Fatalf("status %d, want a new login", rec.Code)
	}
}

func TestOIDC_Logout(t *testing.T) {
	x := newOIDC(t, newFakeOP(t), nil)
	x.login(t)
	rec := x.do(http.MethodGet, "/oauth2/logout", nil)
	if rec.Code != http.StatusFound || !strings.HasSuffix(rec.Header().Get("Location"), "/logout") {
		t.Fatalf("logout: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if x.cookies[oidcSessionCookie] != nil {
		t.Fatal("session not cleared")
	}
	found := false
	for _, e := range *x.events {
		found = found || (e.Event == "oidc_logout" && e.Principal == "alice")
	}
	if !found {
		t.Fatalf("events = %+v", *x.events)
	}
}

func TestOIDC_LogoutWithoutEndSessionGoesHome(t *testing.T) {
	op := newFakeOP(t)
	op.noEndSess = true
	x := newOIDC(t, op, nil)
	if rec := x.do(http.MethodGet, "/oauth2/logout", nil); rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Fatalf("%d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestOIDC_DiscoveryIssuerMustMatch(t *testing.T) {
	op := newFakeOP(t)
	op.wrongIss = true
	x := newOIDC(t, op, nil)
	if rec := x.do(http.MethodGet, "/app", html); rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d, want 502", rec.Code)
	}
}

func TestOIDC_IdPUnreachableIs502(t *testing.T) {
	op := newFakeOP(t)
	x := newOIDC(t, op, nil)
	op.srv.Close()
	if rec := x.do(http.MethodGet, "/app", html); rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestOIDC_ConstructionErrors(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	dir := t.TempDir()
	short := filepath.Join(dir, "short")
	_ = os.WriteFile(short, []byte("short"), 0o600)
	long := filepath.Join(dir, "long")
	_ = os.WriteFile(long, []byte(strings.Repeat("k", 40)), 0o600)
	badCA := filepath.Join(dir, "ca")
	_ = os.WriteFile(badCA, []byte("not pem"), 0o600)
	for name, c := range map[string]config.OIDCConfig{
		"missing key":  {CookieKeyFile: filepath.Join(dir, "none"), RedirectURL: "http://localhost/cb"},
		"short key":    {CookieKeyFile: short, RedirectURL: "http://localhost/cb"},
		"bad redirect": {CookieKeyFile: long, RedirectURL: "://"},
		"missing ca":   {CookieKeyFile: long, RedirectURL: "http://localhost/cb", CAFile: filepath.Join(dir, "none")},
		"bad ca":       {CookieKeyFile: long, RedirectURL: "http://localhost/cb", CAFile: badCA},
	} {
		if _, err := OIDC(c, http.NotFoundHandler(), nil, log, nil); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestOIDC_SecureCookieFollowsTheRedirectScheme(t *testing.T) {
	x := newOIDC(t, newFakeOP(t), nil)
	x.do(http.MethodGet, "/app", html)
	if x.cookies[oidcStateCookie].Secure {
		t.Fatal("http redirect_url on loopback: Secure would lose the cookie")
	}
	y := newOIDC(t, newFakeOP(t), func(c *config.OIDCConfig) { c.RedirectURL = "https://app.example/oauth2/callback" })
	y.do(http.MethodGet, "/app", html)
	if !y.cookies[oidcStateCookie].Secure {
		t.Fatal("https redirect_url: cookie must be Secure")
	}
}

// stubIdP is an issuer whose every response comes from fn.
func stubIdP(t *testing.T, fn func(w http.ResponseWriter, r *http.Request, self string)) *fakeOP {
	t.Helper()
	op := &fakeOP{t: t}
	op.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fn(w, r, op.srv.URL) }))
	t.Cleanup(op.srv.Close)
	return op
}

func TestOIDC_DiscoveryFailures(t *testing.T) {
	doc := func(self string, edit func(map[string]string)) map[string]string {
		d := map[string]string{
			"issuer": self, "authorization_endpoint": self + "/auth",
			"token_endpoint": self + "/token", "jwks_uri": self + "/jwks",
		}
		edit(d)
		return d
	}
	for name, respond := range map[string]func(http.ResponseWriter, *http.Request, string){
		"redirect stays unfollowed": func(w http.ResponseWriter, r *http.Request, self string) {
			if strings.HasSuffix(r.URL.Path, "openid-configuration") {
				http.Redirect(w, r, self+"/elsewhere", http.StatusFound)
				return
			}
			_ = json.NewEncoder(w).Encode(doc(self, func(map[string]string) {}))
		},
		"not json": func(w http.ResponseWriter, _ *http.Request, _ string) { _, _ = w.Write([]byte("not json")) },
		"insecure endpoint": func(w http.ResponseWriter, _ *http.Request, self string) {
			_ = json.NewEncoder(w).Encode(doc(self, func(d map[string]string) { d["token_endpoint"] = "http://idp.example/token" }))
		},
		"no authorization endpoint": func(w http.ResponseWriter, _ *http.Request, self string) {
			_ = json.NewEncoder(w).Encode(doc(self, func(d map[string]string) { delete(d, "authorization_endpoint") }))
		},
	} {
		t.Run(name, func(t *testing.T) {
			x := newOIDC(t, stubIdP(t, respond), nil)
			if _, err := x.handler().discovery(context.Background()); err == nil {
				t.Fatal("want an error")
			}
		})
	}
}

func TestOIDC_DiscoveryBadIssuerURL(t *testing.T) {
	x := newOIDC(t, newFakeOP(t), func(c *config.OIDCConfig) { c.Issuer = "http://bad\x7f" })
	if _, err := x.handler().discovery(context.Background()); err == nil {
		t.Fatal("want an error")
	}
}

func TestOIDC_TokenRequestFailures(t *testing.T) {
	form := func() url.Values { return url.Values{"grant_type": {"refresh_token"}} }
	t.Run("discovery", func(t *testing.T) {
		op := newFakeOP(t)
		x := newOIDC(t, op, nil)
		op.srv.Close()
		if _, err := x.handler().tokenRequest(context.Background(), form()); err == nil {
			t.Fatal("want an error")
		}
	})
	t.Run("bad endpoint", func(t *testing.T) {
		x := newOIDC(t, newFakeOP(t), nil)
		x.presetDiscovery(oidcDiscovery{TokenURL: "http://bad\x7f"})
		if _, err := x.handler().tokenRequest(context.Background(), form()); err == nil {
			t.Fatal("want an error")
		}
	})
	t.Run("unreachable", func(t *testing.T) {
		op := newFakeOP(t)
		x := newOIDC(t, op, nil)
		x.presetDiscovery(oidcDiscovery{TokenURL: op.srv.URL + "/token"})
		op.srv.Close()
		if _, err := x.handler().tokenRequest(context.Background(), form()); err == nil {
			t.Fatal("want an error")
		}
	})
	t.Run("not json", func(t *testing.T) {
		op := stubIdP(t, func(w http.ResponseWriter, _ *http.Request, _ string) { _, _ = w.Write([]byte("not json")) })
		x := newOIDC(t, op, nil)
		x.presetDiscovery(oidcDiscovery{TokenURL: op.srv.URL + "/token"})
		if _, err := x.handler().tokenRequest(context.Background(), form()); err == nil {
			t.Fatal("want an error")
		}
	})
}

// A public client (no secret) sends no Basic credentials.
func TestOIDC_PublicClientSendsNoBasicAuth(t *testing.T) {
	sawBasic := false
	op := stubIdP(t, func(w http.ResponseWriter, r *http.Request, _ string) {
		_, _, sawBasic = r.BasicAuth()
		_, _ = w.Write([]byte(`{"id_token":"t"}`))
	})
	x := newOIDC(t, op, func(c *config.OIDCConfig) { c.ClientSecret = "" })
	x.presetDiscovery(oidcDiscovery{TokenURL: op.srv.URL + "/token"})
	tok, err := x.handler().tokenRequest(context.Background(), url.Values{})
	if err != nil || tok.IDToken != "t" || sawBasic {
		t.Fatalf("tok %+v, err %v, basic %v", tok, err, sawBasic)
	}
}

func TestOIDC_VerifyIDTokenDiscoveryFailure(t *testing.T) {
	op := newFakeOP(t)
	x := newOIDC(t, op, nil)
	op.srv.Close()
	if _, err := x.handler().verifyIDToken(context.Background(), "x"); err == nil {
		t.Fatal("want an error")
	}
}

func TestOIDC_Refresh(t *testing.T) {
	session := func() oidcSession {
		return oidcSession{Sub: "alice", Refresh: "r1", Absolute: time.Now().Add(time.Hour).Unix()}
	}
	t.Run("an unrotated token stays", func(t *testing.T) {
		x := newOIDC(t, newFakeOP(t), nil)
		s, err := x.handler().refresh(context.Background(), session())
		if err != nil || s.Refresh != "r1" {
			t.Fatalf("session %+v, err %v", s, err)
		}
	})
	t.Run("a rotated token replaces it", func(t *testing.T) {
		op := newFakeOP(t)
		op.refresh = "r2"
		x := newOIDC(t, op, nil)
		s, err := x.handler().refresh(context.Background(), session())
		if err != nil || s.Refresh != "r2" {
			t.Fatalf("session %+v, err %v", s, err)
		}
	})
	for name, edit := range map[string]func(*fakeOP){
		"an invalid id token": func(op *fakeOP) { op.badAud = true },
		"another subject":     func(op *fakeOP) { op.sub = "mallory" },
		"no subject":          func(op *fakeOP) { op.sub = "" },
	} {
		t.Run(name, func(t *testing.T) {
			op := newFakeOP(t)
			edit(op)
			x := newOIDC(t, op, nil)
			if _, err := x.handler().refresh(context.Background(), session()); err == nil {
				t.Fatal("want an error")
			}
		})
	}
}

// An ID token with no subject fails the login.
func TestOIDC_CallbackRejectsNoSubject(t *testing.T) {
	op := newFakeOP(t)
	x := newOIDC(t, op, nil)
	loc, err := url.Parse(x.do(http.MethodGet, "/app", html).Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	q := loc.Query()
	op.challenge, op.nonce, op.sub = q.Get("code_challenge"), q.Get("nonce"), ""
	cb := x.do(http.MethodGet, "/oauth2/callback?code=c1&state="+url.QueryEscape(q.Get("state")), html)
	if cb.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", cb.Code)
	}
}

// A refresh token too large for the cookie is dropped; the session itself stays.
func TestOIDC_OversizedRefreshTokenIsDropped(t *testing.T) {
	op := newFakeOP(t)
	op.refresh = strings.Repeat("r", oidcMaxCookieBytes)
	x := newOIDC(t, op, nil)
	x.login(t)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(x.cookies[oidcSessionCookie])
	var s oidcSession
	if !x.handler().readCookie(r, oidcSessionCookie, &s) {
		t.Fatal("want a readable session")
	}
	if s.Sub != "alice" || s.Refresh != "" {
		t.Fatalf("session %+v", s)
	}
}

func TestOIDC_MalformedSessionCookieRestartsLogin(t *testing.T) {
	x := newOIDC(t, newFakeOP(t), nil)
	x.cookies[oidcSessionCookie] = &http.Cookie{Name: oidcSessionCookie, Value: "***"}
	if rec := x.do(http.MethodGet, "/app", html); rec.Code != http.StatusFound {
		t.Fatalf("status %d", rec.Code)
	}
}

// An authorization endpoint that already has a query keeps it.
func TestOIDC_AuthorizationEndpointWithAQuery(t *testing.T) {
	x := newOIDC(t, newFakeOP(t), nil)
	x.presetDiscovery(oidcDiscovery{AuthEndpoint: "https://idp.example/auth?tenant=a"})
	loc, err := url.Parse(x.do(http.MethodGet, "/app", html).Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	q := loc.Query()
	if q.Get("tenant") != "a" || q.Get("response_type") != "code" {
		t.Fatalf("query %v", q)
	}
}

func writeCAFile(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, IsCA: true,
		BasicConstraintsValid: true, NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOIDCClient_CAFile(t *testing.T) {
	ca := writeCAFile(t)
	check := func(t *testing.T) {
		t.Helper()
		c, err := oidcClient(ca)
		if err != nil {
			t.Fatal(err)
		}
		if c.Transport.(*http.Transport).TLSClientConfig.RootCAs == nil {
			t.Fatal("want the CA in the client's pool")
		}
	}
	t.Run("added to the system pool", check)
	t.Run("alone when the system pool is unavailable", func(t *testing.T) {
		orig := oidcSystemCertPool
		oidcSystemCertPool = func() (*x509.CertPool, error) { return nil, errors.New("pool boom") }
		t.Cleanup(func() { oidcSystemCertPool = orig })
		check(t)
	})
}
