package config

import (
	"fmt"
	"net/url"
)

// validateProtocolNotice checks the protocol-rejection settings: the minimum HTTP
// version (HTTP/3 needs the H3 listener), the Alt-Svc lifetime, and the notice URLs.
func validateProtocolNotice(cfg Config) error {
	switch cfg.Security.MinHTTPVersion {
	case "1.1", "2":
	case "3":
		if !cfg.Listeners.H3.Enabled {
			return fmt.Errorf("security.min_http_version 3 needs listeners.h3.enabled")
		}
	default:
		return fmt.Errorf("security.min_http_version must be 1.1, 2 or 3; got %q", cfg.Security.MinHTTPVersion)
	}
	if cfg.Security.AltSvcMaxAge < 0 {
		return fmt.Errorf("security.alt_svc_max_age must not be negative; got %d", cfg.Security.AltSvcMaxAge)
	}
	if b := cfg.TLS.Notice.BaseURL; b != "" {
		u, err := url.Parse(b)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("tls.notice.base_url must be http(s)://host[:port][/path] without a query; got %q", b)
		}
	}
	if h := cfg.TLS.Notice.HelpURL; h != "" {
		u, err := url.Parse(h)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("tls.notice.help_url must be an http(s) URL; got %q", h)
		}
	}
	return nil
}
