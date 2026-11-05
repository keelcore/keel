package config

import (
	"strings"
	"testing"
)

func TestAudit_DefaultsAreOffAndValid(t *testing.T) {
	c := Defaults()
	if c.Audit.Enabled || c.Audit.File != "" {
		t.Fatalf("audit defaults = %+v", c.Audit)
	}
	if err := Validate(c); err != nil {
		t.Fatal(err)
	}
}

func TestValidate_Audit(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*AuditConfig)
		want string
	}{
		{"enabled with a file", func(a *AuditConfig) { a.Enabled = true; a.File = "/a.log" }, ""},
		{"enabled with a remote sink", func(a *AuditConfig) {
			a.Enabled = true
			a.RemoteSink = RemoteSinkConfig{Enabled: true, Endpoint: "https://logs", Protocol: "http"}
		}, ""},
		{"enabled with nowhere to write", func(a *AuditConfig) { a.Enabled = true }, "audit"},
		{"negative rotation", func(a *AuditConfig) { a.Rotation.MaxBackups = -1 }, "max_backups"},
	} {
		c := Defaults()
		tc.edit(&c.Audit)
		err := Validate(c)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: rejected: %v", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: err = %v, want one naming %s", tc.name, err, tc.want)
		}
	}
}

func TestApplyEnv_Audit(t *testing.T) {
	t.Setenv("KEEL_AUDIT_ENABLED", "true")
	t.Setenv("KEEL_AUDIT_FILE", "/a.log")
	c := Defaults()
	applyEnv(&c)
	if !c.Audit.Enabled || c.Audit.File != "/a.log" {
		t.Fatalf("env not applied: %+v", c.Audit)
	}
}
