//go:build !no_oidc && !no_authn

package mw

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
)

func (x *oidcHarness) handler() *oidcHandler { return x.h.(*oidcHandler) }

// presetDiscovery skips the discovery request, with disc as what it found.
func (x *oidcHarness) presetDiscovery(disc oidcDiscovery) {
	o := x.handler()
	o.disc, o.discUntil = &disc, time.Now().Add(time.Hour)
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
