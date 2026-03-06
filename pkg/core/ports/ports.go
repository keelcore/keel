// pkg/core/ports/ports.go
package ports

// Prefer constants for every listener/route binding.
// Env can *disable* a port, or override the constant, but the code always binds explicitly.

const (
	HTTP  = 8080
	HTTPS = 8443
	H3    = 8443 // QUIC/UDP port (if enabled)

	HEALTH  = 9091
	READY   = 9092
	STARTUP = 9093
	ADMIN   = 9999
)
