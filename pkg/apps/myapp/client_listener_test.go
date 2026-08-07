// client_listener_test.go — startDemoClientListener registers, binds, and serves an embedder listener.
package myapp

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	keelconfig "github.com/keelcore/keel/pkg/config"
	keelcore "github.com/keelcore/keel/pkg/core"
	"github.com/keelcore/keel/pkg/core/logging"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func dialable(addr string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if c, err := net.Dial("tcp", addr); err == nil {
			_ = c.Close()
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func newDemoServer(t *testing.T, log *logging.Logger) *keelcore.Server {
	t.Helper()
	return keelcore.NewServer(log, keelconfig.Config{})
}

// Disabled: no env → startDemoClientListener is a no-op.
func TestStartDemoClientListener_Disabled(t *testing.T) {
	t.Setenv("MYAPP_CLIENT_LISTENER_ADDR", "")
	log := logging.New(logging.Config{Out: io.Discard})
	startDemoClientListener(context.Background(), newDemoServer(t, log), log)
}

// Never: the socket never binds; the goroutine returns without calling onBound.
func TestStartDemoClientListener_Never(t *testing.T) {
	t.Setenv("MYAPP_CLIENT_LISTENER_ADDR", freeAddr(t))
	t.Setenv("MYAPP_CLIENT_LISTENER_DELAY", "never")
	log := logging.New(logging.Config{Out: io.Discard})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startDemoClientListener(ctx, newDemoServer(t, log), log)
	time.Sleep(20 * time.Millisecond)
}

// CancelDuringDelay: ctx is cancelled while the bind delay is pending.
func TestStartDemoClientListener_CancelDuringDelay(t *testing.T) {
	t.Setenv("MYAPP_CLIENT_LISTENER_ADDR", freeAddr(t))
	t.Setenv("MYAPP_CLIENT_LISTENER_DELAY", "30s")
	log := logging.New(logging.Config{Out: io.Discard})
	ctx, cancel := context.WithCancel(context.Background())
	startDemoClientListener(ctx, newDemoServer(t, log), log)
	time.Sleep(20 * time.Millisecond)
	cancel()
	time.Sleep(20 * time.Millisecond)
}

// BindsAndServes: after the delay the socket binds, onBound fires, and it serves.
func TestStartDemoClientListener_BindsAndServes(t *testing.T) {
	addr := freeAddr(t)
	t.Setenv("MYAPP_CLIENT_LISTENER_ADDR", addr)
	t.Setenv("MYAPP_CLIENT_LISTENER_DELAY", "10ms")
	log := logging.New(logging.Config{Out: io.Discard})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startDemoClientListener(ctx, newDemoServer(t, log), log)
	if !dialable(addr, 2*time.Second) {
		t.Fatal("client listener never came up")
	}
	resp, err := http.Get("http://" + addr + "/")
	if err != nil {
		t.Fatalf("GET client listener: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "client ok\n" {
		t.Errorf("client listener body = %q, want %q", body, "client ok\n")
	}
	cancel()
	time.Sleep(20 * time.Millisecond)
}

// BindFailure: the target port is occupied, so net.Listen fails and the demo
// listener logs a fatal (ExitFn is a no-op here) and returns.
func TestStartDemoClientListener_BindFailure(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	t.Setenv("MYAPP_CLIENT_LISTENER_ADDR", l.Addr().String())
	t.Setenv("MYAPP_CLIENT_LISTENER_DELAY", "")
	log, _ := newTestLogger(t) // ExitFn no-op: Fatal does not terminate the test
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startDemoClientListener(ctx, newDemoServer(t, log), log)
	time.Sleep(50 * time.Millisecond)
}
