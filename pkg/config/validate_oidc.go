package config

import (
	"fmt"
	"net"
	"net/url"
	"slices"
)

// validateOIDC checks the login flow's settings when it is on. The issuer and redirect must be
// https, or http on loopback for development.
func validateOIDC(o OIDCConfig) error {
	if o.SessionTTL.Duration < 0 {
		return fmt.Errorf("authn.oidc.session_ttl must not be negative (%s)", o.SessionTTL.Duration)
	}
	if !o.Enabled {
		return nil
	}
	if !secureOrLoopback(o.Issuer) {
		return fmt.Errorf("authn.oidc.issuer must be an https URL (http on loopback only); got %q", o.Issuer)
	}
	if o.ClientID == "" {
		return fmt.Errorf("authn.oidc.client_id is required")
	}
	if !secureOrLoopback(o.RedirectURL) {
		return fmt.Errorf("authn.oidc.redirect_url must be an https URL (http on loopback only); got %q", o.RedirectURL)
	}
	if u, err := url.Parse(o.RedirectURL); err != nil || u.Path == "" || u.Path == "/" {
		return fmt.Errorf("authn.oidc.redirect_url needs a callback path; got %q", o.RedirectURL)
	}
	if o.CookieKeyFile == "" {
		return fmt.Errorf("authn.oidc.cookie_key_file is required")
	}
	if !slices.Contains(o.Scopes, "openid") {
		return fmt.Errorf("authn.oidc.scopes must include openid; got %v", o.Scopes)
	}
	return nil
}

func secureOrLoopback(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	if u.Scheme != "http" {
		return false
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	return host == "localhost" || (ip != nil && ip.IsLoopback())
}
