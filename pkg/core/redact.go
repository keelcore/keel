package core

import (
	"net/url"
	"strings"

	"github.com/keelcore/keel/pkg/config"
)

// redacted replaces a secret value in diagnostic output.
const redacted = "[REDACTED]"

// redactedConfig returns a copy of cfg that is safe to write to logs/stderr:
// inline HMAC signer secrets are replaced, and URL credentials (userinfo) and
// query strings — which commonly carry tokens — are stripped. File paths are
// kept: they locate secrets but are not secrets themselves.
func redactedConfig(cfg config.Config) config.Config {
	signers := make([]string, len(cfg.Authn.TrustedSigners))
	for i, s := range cfg.Authn.TrustedSigners {
		if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
			signers[i] = redactURL(s)
		} else {
			signers[i] = redacted
		}
	}
	cfg.Authn.TrustedSigners = signers
	cfg.Sidecar.UpstreamURL = redactURL(cfg.Sidecar.UpstreamURL)
	cfg.ExtAuthz.Endpoint = redactURL(cfg.ExtAuthz.Endpoint)
	cfg.Logging.RemoteSink.Endpoint = redactURL(cfg.Logging.RemoteSink.Endpoint)
	cfg.Tracing.OTLP.Endpoint = redactURL(cfg.Tracing.OTLP.Endpoint)
	cfg.Metrics.StatsD.Endpoint = redactURL(cfg.Metrics.StatsD.Endpoint)
	cfg.TLS.ACME.CAUrl = redactURL(cfg.TLS.ACME.CAUrl)
	return cfg
}

// redactURL strips userinfo and the query string from a URL. A value that does
// not parse as a URL with a scheme (e.g. host:port) is returned unchanged.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || (u.User == nil && u.RawQuery == "") {
		return raw
	}
	if u.User != nil {
		u.User = url.User(redacted)
	}
	if u.RawQuery != "" {
		u.RawQuery = redacted
	}
	return u.String()
}
