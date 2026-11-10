//go:build !no_sidecar

package sidecar

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/ctxkeys"
)

const (
	clientJWT   = "Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJjbGllbnQifQ.c2lnbmF0dXJl"
	keelToken   = "Bearer keel-signed"
	keelTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	keelSpanID  = "00f067aa0ba902b7"
	keelReqID   = "01JD0000000000000000000000"
)

// forged is a request carrying client copies of every header only a hop may create.
func forged() *http.Request {
	r := httptest.NewRequest(http.MethodGet, "http://sidecar.local/x", nil)
	r.RemoteAddr = "203.0.113.7:4321"
	r.Header.Set("Authorization", clientJWT)
	r.Header.Set("X-Real-IP", "6.6.6.6")
	r.Header.Set("X-Forwarded-For", "6.6.6.6")
	r.Header.Set("Forwarded", "for=6.6.6.6;proto=https")
	r.Header.Set("X-Forwarded-Host", "evil.example")
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("Via", "1.1 test-sidecar")
	r.Header.Set("traceparent", "00-ffffffffffffffffffffffffffffffff-ffffffffffffffff-01")
	r.Header.Set("x-request-id", "forged")
	// mw.TraceContext and mw.RequestID run before the proxy and put keel's IDs here.
	ctx := context.WithValue(r.Context(), ctxkeys.RequestID, keelReqID)
	ctx = context.WithValue(ctx, ctxkeys.TraceID, keelTraceID)
	ctx = context.WithValue(ctx, ctxkeys.SpanID, keelSpanID)
	return r.WithContext(ctx)
}

// upstreamSees proxies req through a sidecar in mode, signing or not, and returns
// the headers the upstream received.
func upstreamSees(t *testing.T, req *http.Request, mode string, hops int, signing bool) http.Header {
	t.Helper()
	var got http.Header
	up := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r.Header.Clone() }))
	defer up.Close()
	cfg := config.Config{
		Authn:   config.AuthnConfig{MyID: "test-sidecar"},
		Sidecar: config.SidecarConfig{UpstreamURL: up.URL, XFFMode: mode, XFFTrustedHops: hops},
	}
	var sign []func(*http.Request) error
	if signing {
		sign = append(sign, func(r *http.Request) error { r.Header.Set("Authorization", keelToken); return nil })
	}
	h, err := New(cfg, sign...)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	return got
}

// Class A: every header only a hop may create reaches the upstream as keel's own
// value or not at all, for each xff_mode, with signing on and off.
func TestHopHeaders_ForgedCopiesNeverReachTheUpstream(t *testing.T) {
	for _, mode := range []string{"append", "replace", "strip"} {
		for _, signing := range []bool{true, false} {
			got := upstreamSees(t, forged(), mode, 0, signing)
			name := mode + map[bool]string{true: "/signing", false: "/no-signing"}[signing]
			if v := got.Get("X-Real-IP"); v != "203.0.113.7" {
				t.Errorf("%s: X-Real-IP %q, want the socket peer", name, v)
			}
			for _, h := range []string{"Forwarded", "X-Forwarded-Host", "X-Forwarded-Proto"} {
				if v := got.Values(h); len(v) != 0 {
					t.Errorf("%s: %s = %q, want absent", name, h, v)
				}
			}
			if v := got.Get("x-request-id"); v != keelReqID {
				t.Errorf("%s: x-request-id %q, want keel's %q", name, v, keelReqID)
			}
			if v := got.Get("traceparent"); v != "00-"+keelTraceID+"-"+keelSpanID+"-01" {
				t.Errorf("%s: traceparent %q, want keel's hop", name, v)
			}
			via := got.Values("Via")
			if len(via) == 0 || via[len(via)-1] != "1.1 test-sidecar" || len(via) != 2 {
				t.Errorf("%s: Via %q, want the client's entry then keel's own last", name, via)
			}
			switch mode {
			case "append":
				if v := got.Get("X-Forwarded-For"); v != "6.6.6.6, 203.0.113.7" {
					t.Errorf("%s: X-Forwarded-For %q, want the chain plus the peer", name, v)
				}
			case "replace":
				if v := got.Get("X-Forwarded-For"); v != "203.0.113.7" {
					t.Errorf("%s: X-Forwarded-For %q, want the peer only", name, v)
				}
			case "strip":
				if v := got.Values("X-Forwarded-For"); len(v) != 0 {
					t.Errorf("%s: X-Forwarded-For %q, want absent", name, v)
				}
			}
		}
	}
}

// Class E: with signing on, the upstream sees only keel's token; with signing
// off, the client's JWT arrives byte-identical and keel adds nothing that vouches for it.
func TestHopHeaders_ClientJWT(t *testing.T) {
	if got := upstreamSees(t, forged(), "append", 0, true); got.Get("Authorization") != keelToken || len(got.Values("Authorization")) != 1 {
		t.Errorf("signing on: Authorization %q, want keel's token only", got.Values("Authorization"))
	}
	got := upstreamSees(t, forged(), "append", 0, false)
	if v := got.Values("Authorization"); len(v) != 1 || v[0] != clientJWT {
		t.Errorf("signing off: Authorization %q, want the client's JWT byte-identical", v)
	}
	for k, vv := range got {
		for _, v := range vv {
			if k != "Authorization" && strings.Contains(v, "eyJ") {
				t.Errorf("signing off: %s carries token material %q", k, v)
			}
		}
	}
}

// Class C: repeated X-Forwarded-For lines are one chain: keel's X-Real-IP and the
// single line the upstream receives agree.
func TestHopHeaders_RepeatedXFFLinesAreOneChain(t *testing.T) {
	req := forged()
	req.Header.Del("X-Forwarded-For")
	req.Header.Add("X-Forwarded-For", "1.2.3.4")
	req.Header.Add("X-Forwarded-For", "10.0.0.1")
	got := upstreamSees(t, req, "append", 1, false)
	if v := got.Values("X-Forwarded-For"); len(v) != 1 || v[0] != "1.2.3.4, 10.0.0.1, 203.0.113.7" {
		t.Errorf("X-Forwarded-For %q, want one line with every entry", v)
	}
	if v := got.Get("X-Real-IP"); v != "10.0.0.1" {
		t.Errorf("X-Real-IP %q, want 10.0.0.1 (the entry left of the one trusted hop)", v)
	}
}

func TestViaPseudonym(t *testing.T) {
	for in, want := range map[string]string{
		"":                      "keel",
		"test-sidecar":          "test-sidecar",
		"svc.a_b-1":             "svc.a_b-1",
		"evil, 1.1 keel":        "keel",
		"x\r\nInjected: 1":      "keel",
		strings.Repeat("a", 65): "keel",
	} {
		if got := viaPseudonym(in); got != want {
			t.Errorf("viaPseudonym(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestViaProtocol(t *testing.T) {
	for _, tc := range []struct {
		major, minor int
		want         string
	}{{1, 1, "1.1"}, {1, 0, "1.0"}, {2, 0, "2"}, {3, 0, "3"}} {
		if got := viaProtocol(&http.Request{ProtoMajor: tc.major, ProtoMinor: tc.minor}); got != tc.want {
			t.Errorf("HTTP/%d.%d: %q, want %q", tc.major, tc.minor, got, tc.want)
		}
	}
}
