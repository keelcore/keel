//go:build !no_oidc && !no_authn

package mw

import (
	"time"
)

func (x *oidcHarness) handler() *oidcHandler { return x.h.(*oidcHandler) }

// presetDiscovery skips the discovery request, with disc as what it found.
func (x *oidcHarness) presetDiscovery(disc oidcDiscovery) {
	o := x.handler()
	o.disc, o.discUntil = &disc, time.Now().Add(time.Hour)
}
