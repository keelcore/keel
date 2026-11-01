package mw

// AuthnOption customizes AuthnJWT.
type AuthnOption func(*authnOptions)

type authnOptions struct {
	trustedIDs func() map[string]struct{}
}

// WithTrustedIDs makes AuthnJWT read the allowed subject set on every request
// (nil or empty set: any authenticated subject), so authn.trusted_ids takes
// effect on reload — removing a principal revokes it without a restart.
// Without it, the allowlist is cfg.Authn.TrustedIDs fixed at construction.
func WithTrustedIDs(fn func() map[string]struct{}) AuthnOption {
	return func(o *authnOptions) { o.trustedIDs = fn }
}

// TrustedIDSet builds the allowed-subject set for WithTrustedIDs.
func TrustedIDSet(ids []string) map[string]struct{} {
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}
