//go:build !no_acme

package acme

// Fuzz target for ACME host matching: the host comes from the TLS SNI or the
// HTTP-01 request Host header, both attacker-controlled.

import (
	"strings"
	"testing"
)

func FuzzMatchDomain(f *testing.F) {
	f.Add("example.com", "example.com")
	f.Add("EXAMPLE.com.", "example.com")
	f.Add("evil.example.com", "example.com")
	f.Add("", "")
	f.Add(".", "example.com.")
	f.Fuzz(func(t *testing.T, host, domain string) {
		got, ok := matchDomain(host, []string{domain})
		if !ok {
			return
		}
		if got != domain {
			t.Fatalf("matchDomain(%q) returned %q, want configured domain %q", host, got, domain)
		}
		if !strings.EqualFold(strings.TrimSuffix(host, "."), strings.TrimSuffix(domain, ".")) {
			t.Fatalf("matchDomain matched host %q to unrelated domain %q", host, domain)
		}
	})
}
