// pkg/clisupport/embed.go
// Embedded legal text for --check-integrity.
package clisupport

import _ "embed"

//go:embed LICENSE
var licenseText string

//go:embed TRADEMARK.md
var trademarkText string
