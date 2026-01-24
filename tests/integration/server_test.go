// tests/integration/server_test.go
package integration

import (
    "context"
    "net/http"
    "testing"
    "time"

    "github.com/keelcore/keel/pkg/config"
    "github.com/keelcore/keel/pkg/core"
    "github.com/keelcore/keel/pkg/core/ports"
)

func waitStatus(t *testing.T, url string, want int) {
    t.Helper()
    deadline := time.Now().Add(2 * time.Second)
    for time.Now().Before(deadline) {
        resp, err := http.Get(url) //nolint:gosec
        if err == nil {
            _ = resp.Body.Close()
            if resp.StatusCode == want {
                return
            }
        }
        if resp != nil {
            _ = resp.Body.Close()
        }
        time.Sleep(25 * time.Millisecond)
    }
    t.Fatalf("timeout waiting for %d on %s", want, url)
}

func TestServer_HealthAndDefaultRoot(t *testing.T) {
    cfg := config.Config{
        HTTP: config.Listener{
            Enabled: true,
            Port:    ports.HTTP,
        },
        Health: config.Listener{
            Enabled: true,
            Port:    ports.HEALTH,
        },
        Ready: config.Listener{
            Enabled: true,
            Port:    ports.READY,
        },
        Admin: config.Listener{
            Enabled: false,
            Port:    ports.ADMIN,
        },
        SecurityHeadersEnabled: false,
        AuthnEnabled:           false,
        SheddingEnabled:        false,
        LogJSON:                true,
    }

    srv := core.NewServer(core.WithConfig(cfg), core.WithDefaultRegistrar())

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    go func() { _ = srv.Run(ctx) }()

    mainBase := "http://127.0.0.1:" + itoa(ports.HTTP)
    healthBase := "http://127.0.0.1:" + itoa(ports.HEALTH)
    readyBase := "http://127.0.0.1:" + itoa(ports.READY)

    // Probes on their own ports.
    waitStatus(t, healthBase+"/healthz", 200)
    // ready can be 200 or 503 depending on your Readiness default; accept either by checking it’s not 404.
    waitStatusNot404(t, readyBase+"/readyz")

    // Root on main port.
    waitStatus(t, mainBase+"/", 200)

    // Probes NOT on main port.
    waitStatus(t, mainBase+"/healthz", 404)
    waitStatus(t, mainBase+"/readyz", 404)

    cancel()
}

func waitStatusNot404(t *testing.T, url string) {
    t.Helper()
    deadline := time.Now().Add(2 * time.Second)
    for time.Now().Before(deadline) {
        resp, err := http.Get(url) //nolint:gosec
        if err == nil {
            _ = resp.Body.Close()
            if resp.StatusCode != 404 {
                return
            }
        }
        if resp != nil {
            _ = resp.Body.Close()
        }
        time.Sleep(25 * time.Millisecond)
    }
    t.Fatalf("timeout waiting for non-404 on %s", url)
}

func itoa(n int) string {
    // tiny local itoa to avoid importing strconv in tests
    if n == 0 {
        return "0"
    }
    var b [16]byte
    i := len(b)
    for n > 0 {
        i--
        b[i] = byte('0' + (n % 10))
        n /= 10
    }
    return string(b[i:])
}
