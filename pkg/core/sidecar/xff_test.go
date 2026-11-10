//go:build !no_sidecar

package sidecar

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Class B: delimiters and junk in a client X-Forwarded-For occupy one position
// each and cannot shift X-Real-IP out of the trusted_hops window.
func TestHopHeaders_XFFJunk(t *testing.T) {
	for _, tc := range []struct {
		xff      string
		hops     int
		wantXFF  string
		wantReal string
	}{
		{"1.2.3.4, 10.0.0.1", 1, "1.2.3.4, 10.0.0.1, 203.0.113.7", "10.0.0.1"},
		{"x;y", 1, "unknown, 203.0.113.7", "203.0.113.7"},
		{"1.2.3.4,,10.0.0.1", 1, "1.2.3.4, unknown, 10.0.0.1, 203.0.113.7", "10.0.0.1"},
		{"evil.example, 10.0.0.1", 2, "unknown, 10.0.0.1, 203.0.113.7", "203.0.113.7"},
		{"1.2.3.4\r\nX-Injected: 1", 1, "unknown, 203.0.113.7", "203.0.113.7"},
		{"[2001:db8::1]:443, 10.0.0.1:80", 2, "2001:db8::1, 10.0.0.1, 203.0.113.7", "2001:db8::1"},
		{strings.Repeat("9.9.9.9, ", 200) + "10.0.0.1", 1, strings.Repeat("9.9.9.9, ", 200) + "10.0.0.1, 203.0.113.7", "10.0.0.1"},
	} {
		out := httptest.NewRequest(http.MethodGet, "http://u/", nil)
		applyXFF(out, tc.xff, "203.0.113.7:4321", "append", tc.hops)
		if got := out.Header.Get("X-Forwarded-For"); got != tc.wantXFF {
			t.Errorf("xff %q: X-Forwarded-For %q, want %q", tc.xff, got, tc.wantXFF)
		}
		if got := out.Header.Get("X-Real-IP"); got != tc.wantReal {
			t.Errorf("xff %q hops %d: X-Real-IP %q, want %q", tc.xff, tc.hops, got, tc.wantReal)
		}
	}
}

// realClientIP with trustedHops=0 returns socketPeerIP.
func TestRealClientIP_ZeroHops_ReturnsPeer(t *testing.T) {
	got := realClientIP("1.1.1.1, 2.2.2.2", "3.3.3.3", 0)
	if got != "3.3.3.3" {
		t.Errorf("expected 3.3.3.3, got %q", got)
	}
}

// realClientIP with trustedHops=1 returns the entry immediately to the left of
// the trusted zone.
func TestRealClientIP_OneHop_ReturnsEntry(t *testing.T) {
	// chain: [1.1.1.1, 2.2.2.2, 3.3.3.3(peer)]
	// trustedHops=1 → real client = chain[len-1-1] = chain[1] = 2.2.2.2
	got := realClientIP("1.1.1.1, 2.2.2.2", "3.3.3.3", 1)
	if got != "2.2.2.2" {
		t.Errorf("expected 2.2.2.2, got %q", got)
	}
}

// realClientIP with trustedHops larger than chain length falls back to socketPeerIP.
func TestRealClientIP_TooManyHops_Fallback(t *testing.T) {
	// chain: [3.3.3.3(peer)], trustedHops=5 → idx<0 → fallback
	got := realClientIP("", "3.3.3.3", 5)
	if got != "3.3.3.3" {
		t.Errorf("expected 3.3.3.3 fallback, got %q", got)
	}
}

// realClientIP with empty incomingXFF and trustedHops=1 uses chain=[peer].
func TestRealClientIP_EmptyXFF_OneHop_Fallback(t *testing.T) {
	// chain: [4.4.4.4(peer)], trustedHops=1 → idx=len-2 = -1 → fallback
	got := realClientIP("", "4.4.4.4", 1)
	if got != "4.4.4.4" {
		t.Errorf("expected 4.4.4.4 fallback, got %q", got)
	}
}

// realClientIP with two XFF entries and trustedHops=2 falls back (idx<0).
func TestRealClientIP_TwoXFF_TwoHops_Fallback(t *testing.T) {
	// chain: [1.1.1.1, 3.3.3.3(peer)], trustedHops=2 → idx=2-1-2=-1 → fallback
	got := realClientIP("1.1.1.1", "3.3.3.3", 2)
	if got != "3.3.3.3" {
		t.Errorf("expected 3.3.3.3 fallback, got %q", got)
	}
}

// applyXFF with mode=replace sets XFF to just the socket peer IP.
func TestApplyXFF_Replace(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	applyXFF(req, "1.1.1.1", "2.2.2.2:1234", "replace", 0)
	if got := req.Header.Get("x-forwarded-for"); got != "2.2.2.2" {
		t.Errorf("replace: expected '2.2.2.2', got %q", got)
	}
}

// applyXFF with mode=strip removes XFF.
func TestApplyXFF_Strip(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("x-forwarded-for", "1.1.1.1")
	applyXFF(req, "1.1.1.1", "2.2.2.2:1234", "strip", 0)
	if got := req.Header.Get("x-forwarded-for"); got != "" {
		t.Errorf("strip: expected empty XFF, got %q", got)
	}
}

// applyXFF with mode=append and existing XFF appends the peer.
func TestApplyXFF_Append_WithExisting(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	applyXFF(req, "1.1.1.1", "2.2.2.2:1234", "append", 0)
	if got := req.Header.Get("x-forwarded-for"); got != "1.1.1.1, 2.2.2.2" {
		t.Errorf("append: expected '1.1.1.1, 2.2.2.2', got %q", got)
	}
}

// applyXFF with mode=append and no existing XFF sets just the peer.
func TestApplyXFF_Append_NoExisting(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	applyXFF(req, "", "2.2.2.2:1234", "append", 0)
	if got := req.Header.Get("x-forwarded-for"); got != "2.2.2.2" {
		t.Errorf("append no existing: expected '2.2.2.2', got %q", got)
	}
}

// applyXFF sets x-real-ip.
func TestApplyXFF_SetsXRealIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	applyXFF(req, "", "5.5.5.5:9000", "append", 0)
	if got := req.Header.Get("x-real-ip"); got != "5.5.5.5" {
		t.Errorf("expected x-real-ip=5.5.5.5, got %q", got)
	}
}

// addrToIP with a port splits and returns the host.
func TestAddrToIP_WithPort(t *testing.T) {
	got := addrToIP("192.168.1.1:8080")
	if got != "192.168.1.1" {
		t.Errorf("expected 192.168.1.1, got %q", got)
	}
}

// addrToIP without a port returns addr unchanged.
func TestAddrToIP_NoPort(t *testing.T) {
	got := addrToIP("192.168.1.1")
	if got != "192.168.1.1" {
		t.Errorf("expected 192.168.1.1, got %q", got)
	}
}
