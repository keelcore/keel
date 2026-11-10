//go:build !no_acme

package unit

import (
	"context"
	"testing"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/acme"
)

// Start: returns nil when context is cancelled before any CA contact.
func TestACME_Start_ExitsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately so Start exits without dialling
	mgr := acme.New()
	if err := mgr.Start(ctx, config.ACMEConfig{}); err != nil {
		t.Errorf("Start: expected nil on cancelled context, got %v", err)
	}
}

// Validate: always returns nil.
func TestACME_Validate_ReturnsNil(t *testing.T) {
	if err := acme.Validate(config.Config{}); err != nil {
		t.Errorf("Validate: expected nil, got %v", err)
	}
}
