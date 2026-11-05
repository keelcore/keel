package config

import (
	"strings"
	"testing"
	"time"
)

func validOIDC() OIDCConfig {
	return OIDCConfig{
		Enabled: true, Issuer: "https://idp.example", ClientID: "keel",
		RedirectURL: "https://app.example/oauth2/callback", CookieKeyFile: "/etc/keel/cookie.key",
	}
}

func TestOIDC_DefaultsAreOffAndValid(t *testing.T) {
	c := Defaults()
	o := c.Authn.OIDC
	if o.Enabled || o.SessionTTL.Duration != 8*time.Hour || len(o.Scopes) != 3 {
		t.Fatalf("defaults = %+v", o)
	}
	if err := Validate(c); err != nil {
		t.Fatal(err)
	}
}

func TestValidate_OIDC(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*OIDCConfig)
		want string
	}{
		{"complete", func(*OIDCConfig) {}, ""},
		{"loopback http for development", func(o *OIDCConfig) {
			o.Issuer, o.RedirectURL = "http://127.0.0.1:5556", "http://localhost:8080/oauth2/callback"
		}, ""},
		{"no issuer", func(o *OIDCConfig) { o.Issuer = "" }, "issuer"},
		{"plain http issuer", func(o *OIDCConfig) { o.Issuer = "http://idp.example" }, "issuer"},
		{"no client", func(o *OIDCConfig) { o.ClientID = "" }, "client_id"},
		{"no redirect", func(o *OIDCConfig) { o.RedirectURL = "" }, "redirect_url"},
		{"plain http redirect", func(o *OIDCConfig) { o.RedirectURL = "http://app.example/cb" }, "redirect_url"},
		{"redirect at the root", func(o *OIDCConfig) { o.RedirectURL = "https://app.example/" }, "redirect_url"},
		{"no cookie key", func(o *OIDCConfig) { o.CookieKeyFile = "" }, "cookie_key_file"},
		{"no openid scope", func(o *OIDCConfig) { o.Scopes = []string{"email"} }, "openid"},
		{"negative ttl", func(o *OIDCConfig) { o.SessionTTL = DurationOf(-time.Second) }, "session_ttl"},
	} {
		c := Defaults()
		scopes, ttl := c.Authn.OIDC.Scopes, c.Authn.OIDC.SessionTTL
		c.Authn.OIDC = validOIDC()
		c.Authn.OIDC.Scopes, c.Authn.OIDC.SessionTTL = scopes, ttl
		tc.edit(&c.Authn.OIDC)
		err := Validate(c)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: rejected: %v", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: err = %v, want one naming %s", tc.name, err, tc.want)
		}
	}
}

func TestApplyEnv_OIDC(t *testing.T) {
	t.Setenv("KEEL_OIDC_ENABLED", "true")
	t.Setenv("KEEL_OIDC_ISSUER", "https://idp")
	t.Setenv("KEEL_OIDC_CLIENT_ID", "id")
	t.Setenv("KEEL_OIDC_CLIENT_SECRET", "s")
	t.Setenv("KEEL_OIDC_REDIRECT_URL", "https://a/cb")
	t.Setenv("KEEL_OIDC_COOKIE_KEY_FILE", "/k")
	t.Setenv("KEEL_OIDC_CA_FILE", "/ca")
	c := Defaults()
	applyEnv(&c)
	o := c.Authn.OIDC
	if !o.Enabled || o.Issuer != "https://idp" || o.ClientID != "id" || o.ClientSecret != "s" ||
		o.RedirectURL != "https://a/cb" || o.CookieKeyFile != "/k" || o.CAFile != "/ca" {
		t.Fatalf("env not applied: %+v", o)
	}
}

func TestValidate_OIDCRejectsOtherSchemes(t *testing.T) {
	c := Defaults()
	scopes, ttl := c.Authn.OIDC.Scopes, c.Authn.OIDC.SessionTTL
	c.Authn.OIDC = validOIDC()
	c.Authn.OIDC.Scopes, c.Authn.OIDC.SessionTTL = scopes, ttl
	c.Authn.OIDC.Issuer = "ftp://idp.example"
	if err := Validate(c); err == nil || !strings.Contains(err.Error(), "issuer") {
		t.Fatalf("err = %v", err)
	}
}
