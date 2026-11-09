package knobs

import "strings"

// Error codes (spec/README.md, Errors). The code, layer and key are the contract; Msg is free
// text and never carries a secret value.
const (
	CodeSchemaInvalid          = "schema_invalid"
	CodeModuleConflict         = "module_conflict"
	CodeFileNotFound           = "file_not_found"
	CodeFileParse              = "file_parse"
	CodeUnknownKey             = "unknown_key"
	CodeInvalidType            = "invalid_type"
	CodeOutOfRange             = "out_of_range"
	CodeNotInEnum              = "not_in_enum"
	CodeInvalidLength          = "invalid_length"
	CodeInvalidValue           = "invalid_value" // any other keyword the value breaks (pattern, multipleOf, ...)
	CodeSecretInPlainFile      = "secret_in_plain_file"
	CodeSecretOnCLI            = "secret_on_cli"
	CodeSecretsFilePermissions = "secrets_file_permissions"
	CodeSecretInCode           = "secret_in_code"
	CodeMissingRequired        = "missing_required"
	CodeInvalidArgument        = "invalid_argument"
)

// Error is a configuration error: what went wrong (Code), in which layer, and for which dotted
// key, when it concerns one.
type Error struct {
	Code     string
	Layer    string
	Key      string
	Owner    string // the owner of the key's module, as path@version, when its schema names one
	Msg      string
	Keywords []Keyword // every unrecognized keyword, when a schema fails for them
}

// Keyword is an unrecognized keyword of a schema: its name, and the location of the schema
// object holding it, as a JSON Pointer fragment ("#/properties/port").
type Keyword struct {
	Name     string
	Location string
}

func (e *Error) Error() string {
	parts := []string{"knobs:", e.Code, "(" + e.Layer + ")"}
	if e.Key != "" {
		parts = append(parts, e.Key)
	}
	if e.Owner != "" {
		parts = append(parts, "(owner "+e.Owner+")")
	}
	if e.Msg != "" {
		parts = append(parts, "-", e.Msg)
	}
	return strings.Join(parts, " ")
}

func newError(code, layer, key, msg string) *Error {
	return &Error{Code: code, Layer: layer, Key: key, Msg: msg}
}
