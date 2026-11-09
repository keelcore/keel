package config

import (
	"strings"
	"testing"
)

func TestLogging_FileAndRotationDefaults(t *testing.T) {
	c := Defaults()
	if c.Logging.File != "" {
		t.Fatalf("logging.file = %q, want off by default", c.Logging.File)
	}
	if r := c.Logging.Rotation; r.MaxSizeMB != 100 || r.MaxAgeDays != 30 || r.MaxBackups != 5 || !r.Compress {
		t.Fatalf("rotation defaults = %+v", r)
	}
	if err := Validate(c); err != nil {
		t.Fatal(err)
	}
}

func TestValidate_LogRotation(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*LogRotationConfig)
		want string
	}{
		{"zero size is allowed", func(r *LogRotationConfig) { r.MaxSizeMB = 0 }, ""},
		{"negative size", func(r *LogRotationConfig) { r.MaxSizeMB = -1 }, "max_size_mb"},
		{"negative age", func(r *LogRotationConfig) { r.MaxAgeDays = -1 }, "max_age_days"},
		{"negative backups", func(r *LogRotationConfig) { r.MaxBackups = -1 }, "max_backups"},
	} {
		c := Defaults()
		tc.edit(&c.Logging.Rotation)
		err := Validate(c)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: rejected: %v", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: err = %v, want one naming %s", tc.name, err, tc.want)
		}
	}
}

func TestApplyEnv_LogRotation(t *testing.T) {
	t.Setenv("KEEL_LOGGING_FILE", "/var/log/keel.log")
	t.Setenv("KEEL_LOGGING_ROTATION_MAX_SIZE_MB", "7")
	t.Setenv("KEEL_LOGGING_ROTATION_MAX_AGE_DAYS", "8")
	t.Setenv("KEEL_LOGGING_ROTATION_MAX_BACKUPS", "9")
	t.Setenv("KEEL_LOGGING_ROTATION_COMPRESS", "false")
	c, err := load("", "")
	if err != nil {
		t.Fatal(err)
	}
	r := c.Logging.Rotation
	if c.Logging.File != "/var/log/keel.log" || r.MaxSizeMB != 7 || r.MaxAgeDays != 8 || r.MaxBackups != 9 || r.Compress {
		t.Fatalf("env not applied: %q %+v", c.Logging.File, r)
	}
}
