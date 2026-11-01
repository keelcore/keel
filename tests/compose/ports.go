// tests/compose/ports.go
// Fixed port assignments for the P3 docker-compose.test.yaml topology.
// Test code imports this package to avoid hard-coded port literals.
package compose

const (
	// Keel listeners (docker-compose.test.yaml host-side ports).
	KeelHTTP    = 8080
	KeelHTTPS   = 8443 // TCP; also the H3 QUIC UDP port
	KeelHealth  = 9091
	KeelReady   = 9092
	KeelStartup = 9093
	KeelAdmin   = 9999

	// Upstream echo server.
	Upstream = 9000

	// Observability stack.
	Prometheus = 9090
	OTLPgRPC   = 4317
	OTLPhttp   = 4318
	JaegerUI   = 16686

	// keel-sidecar (keel as sidecar of mockupstream) and the mock's control plane.
	SidecarHTTP  = 8180
	SidecarReady = 9192
	SidecarAdmin = 9199
	MockControl  = 9380
)
