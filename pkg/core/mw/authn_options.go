package mw

import "github.com/keelcore/keel/pkg/core/audit"

// AuthnOption customizes AuthnJWT.
type AuthnOption func(*authnOptions)

type authnOptions struct {
	audit      func(audit.Event)
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

// WithAudit reports each authentication outcome to record.
func WithAudit(record func(audit.Event)) AuthnOption {
	return func(o *authnOptions) { o.audit = record }
}

// record reports e when auditing is on.
func (o authnOptions) record(e audit.Event) {
	if o.audit != nil {
		o.audit(e)
	}
}
