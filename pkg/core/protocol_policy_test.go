package core

import (
	cryptotls "crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/protonotice"
)

func policyServer(edit func(*config.Config)) *Server {
	cfg := config.Defaults()
	cfg.Listeners.HTTP.Enabled = true
	cfg.Listeners.HTTP.Port = 8080
	edit(&cfg)
	return NewServer(logging.New(logging.Config{Out: io.Discard}), cfg)
}

// policyRequest is a request on the HTTPS listener (tls) or the HTTP listener.
func policyRequest(target string, tls bool, major int) *http.Request {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	r.Host = "api.example:8443"
	r.ProtoMajor = major
	r.TLS = nil
	if tls {
		r.TLS = &cryptotls.ConnectionState{}
	}
	return r
}

var reached = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })

func serve(s *Server, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.protocolPolicy(reached).ServeHTTP(rec, r)
	return rec
}

// U12, the governing rule: a protocol the configuration allows is served normally,
// with no Keel-Error-Code.
func TestProtocolPolicy_AllowedProtocolsAreServed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		min   string
		major int
	}{{"default, h1", "1.1", 1}, {"default, h2", "1.1", 2}, {"min 2, h2", "2", 2}, {"min 2, h3", "2", 3}} {
		s := policyServer(func(c *config.Config) { c.Security.MinHTTPVersion = tc.min })
		rec := serve(s, policyRequest("/x", true, tc.major))
		if rec.Code != http.StatusTeapot || rec.Header().Get("Keel-Error-Code") != "" {
			t.Errorf("%s: %d %q, want the handler and no Keel-Error-Code", tc.name, rec.Code, rec.Header().Get("Keel-Error-Code"))
		}
	}
}

// U8: below security.min_http_version, the rejection names the version required.
func TestProtocolPolicy_BelowMinimumHTTPVersion(t *testing.T) {
	for _, tc := range []struct {
		min      string
		major    int
		required string
	}{{"2", 1, "HTTP/2"}, {"3", 2, "HTTP/3"}, {"3", 1, "HTTP/3"}} {
		s := policyServer(func(c *config.Config) { c.Security.MinHTTPVersion = tc.min })
		rec := serve(s, policyRequest("/x", true, tc.major))
		if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Keel-Error-Code") != string(protonotice.HTTPVersionBelowMin) {
			t.Fatalf("min %s, HTTP/%d: %d %q, want 301 KEEL-PROTO-007", tc.min, tc.major, rec.Code, rec.Header().Get("Keel-Error-Code"))
		}
		if loc := rec.Header().Get("Location"); loc != "http://api.example:8080/.well-known/keel/upgrade?code=KEEL-PROTO-007" {
			t.Errorf("Location %q", loc)
		}
		var p struct{ Required []string }
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || len(p.Required) != 1 || p.Required[0] != tc.required {
			t.Errorf("min %s: required %v, want [%s]", tc.min, p.Required, tc.required)
		}
		if !strings.Contains(scrape(s.met), `keel_protocol_rejections_total{code="KEEL-PROTO-007"} 1`) {
			t.Errorf("rejection not counted")
		}
	}
}

// The minimum applies to HTTPS and H3; the plain HTTP listener is never subject to it.
func TestProtocolPolicy_PlainHTTPIsNotSubjectToTheMinimum(t *testing.T) {
	s := policyServer(func(c *config.Config) { c.Security.MinHTTPVersion = "2" })
	if rec := serve(s, policyRequest("/x", false, 1)); rec.Code != http.StatusTeapot {
		t.Fatalf("plain HTTP: %d, want the handler", rec.Code)
	}
}

// U5: the notice page is served on the plain HTTP listener, ahead of every other
// route and of authentication, and counted.
func TestProtocolPolicy_ServesTheNotice(t *testing.T) {
	s := policyServer(func(c *config.Config) {})
	rec := serve(s, policyRequest(protonotice.NoticePath+"?code=KEEL-PROTO-001&format=json", false, 1))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != protonotice.ContentType {
		t.Fatalf("notice: %d %q, want 200 problem+json", rec.Code, rec.Header().Get("Content-Type"))
	}
	if !strings.Contains(scrape(s.met), `keel_protocol_notices_served_total{code="KEEL-PROTO-001",format="json"} 1`) {
		t.Errorf("notice not counted")
	}
	if rec := serve(s, policyRequest(protonotice.NoticePath+"?code=KEEL-PROTO-001", true, 2)); rec.Code != http.StatusTeapot {
		t.Errorf("notice path over HTTPS: %d, want the handler (served on plain HTTP only)", rec.Code)
	}
}

// U10: with tls.client_cert_notice, a request without a verified client chain gets
// KEEL-PROTO-005; one with a chain is served.
func TestProtocolPolicy_ClientCertNotice(t *testing.T) {
	s := policyServer(func(c *config.Config) {
		c.TLS.ClientCAFile = "ca.pem"
		c.TLS.ClientCertNotice = true
	})
	if rec := serve(s, policyRequest("/x", true, 2)); rec.Code != http.StatusMovedPermanently ||
		rec.Header().Get("Keel-Error-Code") != string(protonotice.ClientCertRequired) {
		t.Fatalf("no client cert: %d %q, want 301 KEEL-PROTO-005", rec.Code, rec.Header().Get("Keel-Error-Code"))
	}
	r := policyRequest("/x", true, 2)
	r.TLS.VerifiedChains = [][]*x509.Certificate{{{}}}
	if rec := serve(s, r); rec.Code != http.StatusTeapot {
		t.Fatalf("verified client cert: %d, want the handler", rec.Code)
	}
	off := policyServer(func(c *config.Config) { c.TLS.ClientCAFile = "ca.pem" })
	if rec := serve(off, policyRequest("/x", true, 2)); rec.Code != http.StatusTeapot {
		t.Fatalf("notice off: %d, want the handler (the handshake enforces the certificate)", rec.Code)
	}
}

// U10: the notice relaxes only the missing-certificate case: a presented
// certificate is still verified during the handshake.
func TestApplyClientCertNotice(t *testing.T) {
	cfg := &cryptotls.Config{ClientAuth: cryptotls.RequireAndVerifyClientCert}
	applyClientCertNotice(cfg, true)
	if cfg.ClientAuth != cryptotls.VerifyClientCertIfGiven {
		t.Fatalf("ClientAuth %v, want VerifyClientCertIfGiven", cfg.ClientAuth)
	}
	cfg = &cryptotls.Config{ClientAuth: cryptotls.RequireAndVerifyClientCert}
	applyClientCertNotice(cfg, false)
	if cfg.ClientAuth != cryptotls.RequireAndVerifyClientCert {
		t.Fatalf("notice off changed ClientAuth to %v", cfg.ClientAuth)
	}
	cfg = &cryptotls.Config{}
	applyClientCertNotice(cfg, true)
	if cfg.ClientAuth != cryptotls.NoClientCert {
		t.Fatalf("without mTLS, ClientAuth %v, want NoClientCert", cfg.ClientAuth)
	}
}

// U4: the notice is reached at tls.notice.base_url when set, else on the HTTP
// listener of the host the client asked for; with neither there is no notice URL.
func TestNoticeBase(t *testing.T) {
	cfg := config.Defaults()
	cfg.Listeners.HTTP.Enabled = true
	cfg.Listeners.HTTP.Port = 8080
	for _, tc := range []struct {
		name, base, host string
		httpOn           bool
		want             string
	}{
		{"configured base", "https://notice.example/", "api.example:8443", true, "https://notice.example"},
		{"request host", "", "api.example:8443", true, "http://api.example:8080"},
		{"host without port", "", "api.example", true, "http://api.example:8080"},
		{"ipv6 host", "", "[2001:db8::1]:8443", true, "http://[2001:db8::1]:8080"},
		{"no http listener", "", "api.example:8443", false, ""},
		{"hostile host", "", "evil.example/x?y", true, ""},
	} {
		c := cfg
		c.TLS.Notice.BaseURL = tc.base
		c.Listeners.HTTP.Enabled = tc.httpOn
		if got := noticeBase(c, tc.host); got != tc.want {
			t.Errorf("%s: noticeBase = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// An empty or over-long Host yields no notice URL rather than a malformed Location.
func TestNoticeBase_RefusesEmptyAndOverlongHosts(t *testing.T) {
	cfg := config.Defaults()
	cfg.Listeners.HTTP.Enabled = true
	for _, host := range []string{"", strings.Repeat("a", 254)} {
		if got := noticeBase(cfg, host); got != "" {
			t.Errorf("host of %d bytes: noticeBase = %q, want \"\"", len(host), got)
		}
	}
}
