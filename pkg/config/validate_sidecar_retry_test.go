package config

import (
	"strings"
	"testing"
	"time"
)

// sidecar.retry: defaults are valid; a non-positive attempt count or a negative
// backoff is rejected only when the sidecar is enabled.
func TestValidate_SidecarRetry(t *testing.T) {
	base := Defaults()
	base.Sidecar.Enabled = true
	base.Sidecar.UpstreamURL = "http://app:8080"
	if base.Sidecar.Retry.DialAttempts != 3 || base.Sidecar.Retry.DialBackoff.Duration != 50*time.Millisecond {
		t.Fatalf("defaults = %+v", base.Sidecar.Retry)
	}
	if err := Validate(base); err != nil {
		t.Fatalf("defaults rejected: %v", err)
	}

	zero := base
	zero.Sidecar.Retry.DialAttempts = 0
	if err := Validate(zero); err == nil || !strings.Contains(err.Error(), "dial_attempts") {
		t.Errorf("dial_attempts 0: err = %v", err)
	}

	neg := base
	neg.Sidecar.Retry.DialBackoff = DurationOf(-time.Millisecond)
	if err := Validate(neg); err == nil || !strings.Contains(err.Error(), "dial_backoff") {
		t.Errorf("negative dial_backoff: err = %v", err)
	}

	off := zero
	off.Sidecar.Enabled = false
	off.Sidecar.Retry.DialBackoff = DurationOf(-time.Millisecond)
	if err := Validate(off); err != nil {
		t.Errorf("sidecar disabled: retry fields must not be validated: %v", err)
	}
}

// KEEL_SIDECAR_RETRY_DIAL_ATTEMPTS / KEEL_SIDECAR_RETRY_DIAL_BACKOFF override the YAML values.
func TestApplyEnv_SidecarRetry(t *testing.T) {
	t.Setenv("KEEL_SIDECAR_RETRY_DIAL_ATTEMPTS", "5")
	t.Setenv("KEEL_SIDECAR_RETRY_DIAL_BACKOFF", "200ms")
	cfg, err := load("", "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sidecar.Retry.DialAttempts != 5 || cfg.Sidecar.Retry.DialBackoff.Duration != 200*time.Millisecond {
		t.Fatalf("env overrides not applied: %+v", cfg.Sidecar.Retry)
	}
}
