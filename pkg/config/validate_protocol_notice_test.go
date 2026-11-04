package config

import (
	"strings"
	"testing"
)

// Protocol notices (docs/security.md, "Protocol rejections"): defaults are valid
// and change no behavior; each field is validated.
func TestValidate_ProtocolNotice(t *testing.T) {
	base := Defaults()
	if base.Security.MinHTTPVersion != "1.1" || base.Security.AltSvcMaxAge != 86400 || base.TLS.ClientCertNotice ||
		base.TLS.Notice != (NoticeConfig{}) {
		t.Fatalf("defaults: min_http_version %q alt_svc_max_age %d client_cert_notice %v notice %+v",
			base.Security.MinHTTPVersion, base.Security.AltSvcMaxAge, base.TLS.ClientCertNotice, base.TLS.Notice)
	}
	if err := Validate(base); err != nil {
		t.Fatalf("defaults rejected: %v", err)
	}

	for _, tc := range []struct {
		name string
		edit func(*Config)
		want string // "" → valid
	}{
		{"min empty means 1.1", func(c *Config) { c.Security.MinHTTPVersion = "" }, ""},
		{"min 2", func(c *Config) { c.Security.MinHTTPVersion = "2" }, ""},
		{"min 3 with h3", func(c *Config) {
			c.Security.MinHTTPVersion = "3"
			c.Listeners.H3.Enabled = true
			c.TLS.CertFile, c.TLS.KeyFile = "c.pem", "k.pem"
		}, ""},
		{"min 3 without h3", func(c *Config) { c.Security.MinHTTPVersion = "3" }, "min_http_version"},
		{"min 1.0", func(c *Config) { c.Security.MinHTTPVersion = "1.0" }, "min_http_version"},
		{"negative alt-svc", func(c *Config) { c.Security.AltSvcMaxAge = -1 }, "alt_svc_max_age"},
		{"base_url https", func(c *Config) { c.TLS.Notice.BaseURL = "https://notice.example" }, ""},
		{"base_url relative", func(c *Config) { c.TLS.Notice.BaseURL = "/notice" }, "base_url"},
		{"base_url with query", func(c *Config) { c.TLS.Notice.BaseURL = "http://n.example/?x=1" }, "base_url"},
		{"help_url ftp", func(c *Config) { c.TLS.Notice.HelpURL = "ftp://help.example" }, "help_url"},
		{"help_url https", func(c *Config) { c.TLS.Notice.HelpURL = "https://help.example/upgrade" }, ""},
	} {
		c := Defaults()
		tc.edit(&c)
		err := Validate(c)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: rejected: %v", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: err = %v, want one naming %s", tc.name, err, tc.want)
		}
	}
}

func TestApplyEnv_ProtocolNotice(t *testing.T) {
	t.Setenv("KEEL_MIN_HTTP_VERSION", "2")
	t.Setenv("KEEL_ALT_SVC_MAX_AGE", "60")
	t.Setenv("KEEL_TLS_NOTICE_BASE_URL", "https://n.example")
	t.Setenv("KEEL_TLS_NOTICE_HELP_URL", "https://h.example")
	t.Setenv("KEEL_TLS_NOTICE_CONTACT", "ops@example.com")
	t.Setenv("KEEL_TLS_CLIENT_CERT_NOTICE", "true")
	c := Defaults()
	applyEnv(&c)
	if c.Security.MinHTTPVersion != "2" || c.Security.AltSvcMaxAge != 60 || !c.TLS.ClientCertNotice ||
		c.TLS.Notice != (NoticeConfig{BaseURL: "https://n.example", HelpURL: "https://h.example", Contact: "ops@example.com"}) {
		t.Fatalf("env not applied: %+v %+v", c.Security, c.TLS)
	}
}

// A config built without Defaults() still reaches every other check: the protocol
// settings' zero values are their defaults.
func TestValidate_ZeroConfigReachesTheOtherChecks(t *testing.T) {
	cfg := Config{}
	cfg.TLS.ACME.Enabled = true
	cfg.TLS.ACME.Domains = []string{"example.com"}
	cfg.TLS.ACME.ChallengePort = 8080
	t.Setenv("BATS_TEST_FILENAME", "")
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "challenge_port") {
		t.Fatalf("err = %v, want the challenge_port check", err)
	}
}
