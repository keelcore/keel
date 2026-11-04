// pkg/core/protocol_policy.go
package core

import (
	cryptotls "crypto/tls"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/protonotice"
)

// protocolPolicy answers, ahead of authentication, the requests keel rejects for
// their protocol, and serves the notice page those rejections point at
// (docs/security.md, "Protocol rejections"). A request the configuration allows
// reaches next unchanged.
func (s *Server) protocolPolicy(next http.Handler) http.Handler {
	cfg := s.cfg
	minMajor := minHTTPMajor(cfg.Security.MinHTTPVersion)
	certNotice := cfg.TLS.ClientCertNotice && cfg.TLS.ClientCAFile != ""
	notice := protonotice.NoticeHandler(protonotice.NoticeOptions{HelpURL: cfg.TLS.Notice.HelpURL, Contact: cfg.TLS.Notice.Contact},
		func(c protonotice.Code, format string) { s.met.IncProtocolNotice(string(c), format) })
	reject := func(w http.ResponseWriter, r *http.Request, code protonotice.Code, required ...string) {
		s.met.IncProtocolRejection(string(code))
		protonotice.WriteRejection(w, r, code, noticeBase(cfg, r.Host), required...)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.TLS == nil && r.URL.Path == protonotice.NoticePath && (r.Method == http.MethodGet || r.Method == http.MethodHead):
			notice.ServeHTTP(w, r)
		case r.TLS != nil && certNotice && len(r.TLS.VerifiedChains) == 0:
			reject(w, r, protonotice.ClientCertRequired)
		case r.TLS != nil && r.ProtoMajor < minMajor:
			reject(w, r, protonotice.HTTPVersionBelowMin, "HTTP/"+strconv.Itoa(minMajor))
		default:
			next.ServeHTTP(w, r)
		}
	})
}

// minHTTPMajor is the HTTP major version security.min_http_version requires.
func minHTTPMajor(v string) int {
	switch v {
	case "2":
		return 2
	case "3":
		return 3
	}
	return 1
}

// noticeBase is the scheme://host[:port] serving the notice page: tls.notice.base_url,
// else the HTTP listener on the host the client asked for; "" when neither exists
// (the rejection is then answered in place).
func noticeBase(cfg config.Config, host string) string {
	if b := cfg.TLS.Notice.BaseURL; b != "" {
		return strings.TrimRight(b, "/")
	}
	if !cfg.Listeners.HTTP.Enabled {
		return ""
	}
	name := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		name = h
	}
	if !validHostname(name) {
		return ""
	}
	if strings.Contains(name, ":") {
		name = "[" + name + "]"
	}
	return "http://" + name + ":" + strconv.Itoa(cfg.Listeners.HTTP.Port)
}

// validHostname: a DNS name or IP literal only, so a Host header cannot steer the
// Location anywhere but a host of that name.
func validHostname(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	if net.ParseIP(h) != nil {
		return true
	}
	for i := 0; i < len(h); i++ {
		c := h[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '.':
		default:
			return false
		}
	}
	return true
}

// applyClientCertNotice (tls.client_cert_notice) completes handshakes that carry
// no client certificate, so protocolPolicy can answer them with KEEL-PROTO-005;
// a presented certificate is still verified against client_ca_file.
func applyClientCertNotice(tlsCfg *cryptotls.Config, enabled bool) {
	if enabled && tlsCfg.ClientAuth == cryptotls.RequireAndVerifyClientCert {
		tlsCfg.ClientAuth = cryptotls.VerifyClientCertIfGiven
	}
}
