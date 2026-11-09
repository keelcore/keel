// Package knobs is a layered configuration engine. An application names itself once (New),
// attaches one JSON Schema per module (Attach), and adds layers in precedence order, lowest
// first (Add, or AddStandard for the standard set):
//
//	code → schema default → global file → user file → secrets file → env → cli → runtime → connection
//
// Every layer is validated against the schemas before it is merged, so an error names its
// layer and key; a schema may hold only its draft's keywords and the x-knobs- ones, so no rule in it goes
// unenforced. A value read from the secrets file, or set for a key the schema marks secret,
// is secret: it prints as Redacted in every dump and audit record and is read only through
// Secret.Reveal. Connection overrides apply per connection (Config.WithConnection), on
// allowlisted keys, when the connection is authorized, clamped to the schema's bounds; the
// application maps whatever the caller sends to connection values, and knobs keeps no state
// between connections.
//
// The contract every knobs implementation follows, in every language, is spec/README.md in
// the repository; spec/cases holds its conformance cases.
package knobs
