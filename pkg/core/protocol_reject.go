// pkg/core/protocol_reject.go
package core

import (
	cryptotls "crypto/tls"
	stdlog "log"
	"strings"
	"sync"
	"time"

	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/metrics"
	"github.com/keelcore/keel/pkg/core/protonotice"
)

// rejectFunc observes a ClientHello the listener policy refuses (keeltls.ObserveRejections).
type rejectFunc func(protonotice.Code, *cryptotls.ClientHelloInfo)

// handshakeRejectionLog counts every refused handshake by code and logs it as
// tls_handshake_rejected — at most perSecond lines a second, so a scan cannot
// flood the log pipeline — with what the client offered, so an operator can tell
// the client owner what to upgrade.
func handshakeRejectionLog(log *logging.Logger, met *metrics.Metrics, perSecond int) rejectFunc {
	sample := newSampler(perSecond)
	return func(code protonotice.Code, hello *cryptotls.ClientHelloInfo) {
		met.IncTLSHandshakeRejection(string(code))
		if !sample() {
			return
		}
		e, _ := protonotice.Lookup(code)
		fields := map[string]any{
			"code":               string(code),
			"reason":             e.Reason,
			"client_max_version": tlsVersionName(maxVersion(hello.SupportedVersions)),
			"offered_alpn":       hello.SupportedProtos,
			"sni":                hello.ServerName,
		}
		if hello.Conn != nil {
			fields["remote"] = hello.Conn.RemoteAddr().String()
		}
		log.Info("tls_handshake_rejected", fields)
	}
}

// newSampler allows up to perSecond events in each one-second window.
func newSampler(perSecond int) func() bool {
	var mu sync.Mutex
	var window time.Time
	var n int
	return func() bool {
		mu.Lock()
		defer mu.Unlock()
		now := time.Now().Truncate(time.Second)
		if !now.Equal(window) {
			window, n = now, 0
		}
		n++
		return n <= perSecond
	}
}

func maxVersion(versions []uint16) uint16 {
	var v uint16
	for _, x := range versions {
		v = max(v, x)
	}
	return v
}

func tlsVersionName(v uint16) string {
	if v == 0 {
		return "unknown"
	}
	return cryptotls.VersionName(v)
}

// serverErrorLog routes net/http's ErrorLog (handshake failures, malformed
// requests) into the structured logger at debug, instead of stderr.
func serverErrorLog(log *logging.Logger) *stdlog.Logger {
	return stdlog.New(errorLogWriter{log}, "", 0)
}

type errorLogWriter struct{ log *logging.Logger }

func (w errorLogWriter) Write(p []byte) (int, error) {
	w.log.Debug("http_server_error", map[string]any{"err": strings.TrimRight(string(p), "\n")})
	return len(p), nil
}

// rejectionLogsPerSecond caps tls_handshake_rejected log lines; the counter sees every one.
const rejectionLogsPerSecond = 10
