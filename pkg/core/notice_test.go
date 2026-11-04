package core

import (
	"strings"
	"testing"

	"github.com/keelcore/keel/pkg/config"
)

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
