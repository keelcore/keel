package config

import (
	"strings"
	"testing"
	"time"
)

// ext_authz with no endpoint or a non-positive timeout would fail every call,
// which fail_open turns into allow-all; Validate rejects both.
func TestValidate_ExtAuthzRequiresEndpointAndTimeout(t *testing.T) {
	base := Defaults()
	base.ExtAuthz.Enabled = true
	base.ExtAuthz.Endpoint = "http://127.0.0.1:8181/v1/data/authz/allow"
	base.ExtAuthz.Timeout = DurationOf(500 * time.Millisecond)
	if err := Validate(base); err != nil {
		t.Fatalf("valid ext_authz config rejected: %v", err)
	}

	noEndpoint := base
	noEndpoint.ExtAuthz.Endpoint = ""
	if err := Validate(noEndpoint); err == nil || !strings.Contains(err.Error(), "endpoint") {
		t.Errorf("empty endpoint: err = %v", err)
	}

	zeroTimeout := base
	zeroTimeout.ExtAuthz.Timeout = DurationOf(0)
	if err := Validate(zeroTimeout); err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Errorf("zero timeout: err = %v", err)
	}
}
