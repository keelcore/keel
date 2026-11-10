package core

import (
	"bytes"
	cryptotls "crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keelcore/keel/pkg/core/audit"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/metrics"
	"github.com/keelcore/keel/pkg/core/protonotice"
)

// scrape is the metrics exposition.
func scrape(met *metrics.Metrics) string {
	rec := httptest.NewRecorder()
	met.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return rec.Body.String()
}

func logLines(t *testing.T, out string) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("log line %q: %v", l, err)
		}
		lines = append(lines, m)
	}
	return lines
}

// U2: a rejected handshake is logged with its code and what the client offered,
// and counted.
func TestHandshakeRejectionLog(t *testing.T) {
	var buf bytes.Buffer
	log := logging.New(logging.Config{JSON: true, Out: &buf})
	met := metrics.New()
	observe := handshakeRejectionLog(log, met, 10)
	observe(protonotice.TLSVersion, &cryptotls.ClientHelloInfo{
		SupportedVersions: []uint16{cryptotls.VersionTLS12, cryptotls.VersionTLS11},
		SupportedProtos:   []string{"http/1.1"},
		ServerName:        "api.example",
	})
	lines := logLines(t, buf.String())
	if len(lines) != 1 {
		t.Fatalf("%d log lines, want 1:\n%s", len(lines), buf.String())
	}
	for k, want := range map[string]any{
		"msg":                "tls_handshake_rejected",
		"code":               "KEEL-PROTO-001",
		"reason":             "tls_version_below_1_3",
		"client_max_version": "TLS 1.2",
		"sni":                "api.example",
	} {
		if lines[0][k] != want {
			t.Errorf("%s = %v, want %v", k, lines[0][k], want)
		}
	}
	if alpn, _ := lines[0]["offered_alpn"].([]any); len(alpn) != 1 || alpn[0] != "http/1.1" {
		t.Errorf("offered_alpn = %v, want [http/1.1]", lines[0]["offered_alpn"])
	}
	exp := scrape(met)
	if !strings.Contains(exp, `keel_tls_handshake_rejections_total{code="KEEL-PROTO-001"} 1`) {
		t.Errorf("rejection not counted:\n%s", exp)
	}
}

// U2: logging is sampled to protect the pipeline; every rejection is still counted.
func TestHandshakeRejectionLog_Sampled(t *testing.T) {
	var buf bytes.Buffer
	log := logging.New(logging.Config{JSON: true, Out: &buf})
	met := metrics.New()
	observe := handshakeRejectionLog(log, met, 5)
	for range 100 {
		observe(protonotice.NoSharedALPN, &cryptotls.ClientHelloInfo{})
	}
	if n := len(logLines(t, buf.String())); n > 5 || n == 0 {
		t.Fatalf("%d log lines for 100 rejections in one second, want 1..5", n)
	}
	exp := scrape(met)
	if !strings.Contains(exp, `keel_tls_handshake_rejections_total{code="KEEL-PROTO-002"} 100`) {
		t.Errorf("want all 100 counted:\n%s", exp)
	}
}

// U2: net/http's ErrorLog lines go to the structured logger, not stderr.
func TestServerErrorLog_IsStructured(t *testing.T) {
	var buf bytes.Buffer
	log := logging.New(logging.Config{JSON: true, Out: &buf, Level: "debug"})
	serverErrorLog(log).Printf("http: TLS handshake error from 192.0.2.1:5555: EOF")
	lines := logLines(t, buf.String())
	if len(lines) != 1 || lines[0]["msg"] != "http_server_error" || lines[0]["err"] != "http: TLS handshake error from 192.0.2.1:5555: EOF" {
		t.Fatalf("got %v, want one http_server_error entry", lines)
	}
}

// The rejection log names the client's address when the hello carries its connection.
func TestHandshakeRejectionLog_RemoteAddress(t *testing.T) {
	var buf bytes.Buffer
	log := logging.New(logging.Config{JSON: true, Out: &buf})
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	handshakeRejectionLog(log, metrics.New(), 10)(protonotice.NoSharedALPN, &cryptotls.ClientHelloInfo{Conn: server})
	lines := logLines(t, buf.String())
	if len(lines) != 1 || lines[0]["remote"] != server.RemoteAddr().String() {
		t.Fatalf("got %v, want remote %q", lines, server.RemoteAddr().String())
	}
}

// Refused handshakes reach the stream at a bounded rate, with the source address when known.
func TestAudit_HandshakeRejectionSampledWithSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	s := auditServer(t, path, nil)
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	observe := s.auditedRejections(func(protonotice.Code, *cryptotls.ClientHelloInfo) {})
	for range rejectionLogsPerSecond * 5 {
		observe(protonotice.TLSVersion, &cryptotls.ClientHelloInfo{Conn: server})
	}
	n, err := audit.VerifyFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 || n >= rejectionLogsPerSecond*5 {
		t.Fatalf("%d entries for %d rejections", n, rejectionLogsPerSecond*5)
	}
	if !strings.Contains(auditText(t, path), `"source_ip":"pipe"`) {
		t.Fatalf("audit %q", auditText(t, path))
	}
}

func TestAddrIP(t *testing.T) {
	if got := addrIP(&net.TCPAddr{IP: net.IPv4(203, 0, 113, 7), Port: 443}); got != "203.0.113.7" {
		t.Errorf("TCP address: %q", got)
	}
	if got := addrIP(&net.UnixAddr{Name: "sock"}); got != "sock" {
		t.Errorf("no port: %q", got)
	}
}
