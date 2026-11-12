// tests/mockupstream/mock/mock_test.go
// Unit tests for mock.go. No socket is bound: the listen and runServer seams
// stand in for net.Listen and keel's run loop, and connections are net.Pipe.
package mock

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	keelcore "github.com/keelcore/keel/pkg/core"
	"github.com/keelcore/keel/pkg/core/logging"
)

// errFatalSentinel is panicked by the test logger's ExitFn so a Fatal call
// unwinds to expectFatal instead of terminating the test process.
var errFatalSentinel = errors.New("fatal-sentinel")

// quietLogger discards output; fatalLogger also panics errFatalSentinel on Fatal.
func quietLogger() *logging.Logger {
	return logging.New(logging.Config{JSON: true, Out: io.Discard})
}

func fatalLogger() *logging.Logger {
	log := quietLogger()
	log.ExitFn = func(int) { panic(errFatalSentinel) }
	return log
}

// expectFatal asserts fn triggers a logger.Fatal (the sentinel panic).
func expectFatal(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected a Fatal (sentinel panic) but none occurred")
		}
		if r != errFatalSentinel {
			panic(r)
		}
	}()
	fn()
}

// fakeListener hands Accept the queued results in order; after Close, Accept
// returns net.ErrClosed.
type fakeListener struct {
	accepts chan acceptResult
	closed  chan struct{}
	once    sync.Once
}

type acceptResult struct {
	conn net.Conn
	err  error
}

func newFakeListener() *fakeListener {
	return &fakeListener{accepts: make(chan acceptResult, 4), closed: make(chan struct{})}
}

func (l *fakeListener) Accept() (net.Conn, error) {
	select {
	case r := <-l.accepts:
		return r.conn, r.err
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *fakeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *fakeListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9000} }

// stubListen points the listen seam at ln (or err) and records each address asked for.
func stubListen(t *testing.T, ln net.Listener, err error) *[]string {
	t.Helper()
	var addrs []string
	orig := listen
	listen = func(network, addr string) (net.Listener, error) {
		if network != "tcp" {
			t.Errorf("network = %q, want tcp", network)
		}
		addrs = append(addrs, addr)
		return ln, err
	}
	t.Cleanup(func() { listen = orig })
	return &addrs
}

// stubLogger points the newLogger seam at log.
func stubLogger(t *testing.T, log *logging.Logger) {
	t.Helper()
	orig := newLogger
	newLogger = func() *logging.Logger { return log }
	t.Cleanup(func() { newLogger = orig })
}

// stubRunServer replaces keel's run loop with one that returns at once.
func stubRunServer(t *testing.T) *int {
	t.Helper()
	calls := 0
	orig := runServer
	runServer = func(*keelcore.Server, context.Context) { calls++ }
	t.Cleanup(func() { runServer = orig })
	return &calls
}

func newTestUpstream() *upstream {
	return &upstream{addr: "fake:9000", mode: modeOK, log: quietLogger(), conns: map[net.Conn]struct{}{}}
}

// waitClosed fails the test unless ch closes within a few seconds.
func waitClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestNewLogger_ReturnsALoggerThatExitsOnFatal(t *testing.T) {
	log := newLogger()
	if log == nil || log.ExitFn == nil {
		t.Fatalf("newLogger() = %v, want a logger with an ExitFn", log)
	}
}

func TestRun_BindsTheUpstreamAndRunsKeelUntilStopped(t *testing.T) {
	t.Setenv("MOCK_UPSTREAM_ADDR", ":9300")
	stubLogger(t, quietLogger())
	ln := newFakeListener()
	addrs := stubListen(t, ln, nil)
	calls := stubRunServer(t)
	if got := Run(); got != 0 {
		t.Fatalf("Run() = %d, want 0", got)
	}
	if *calls != 1 {
		t.Fatalf("runServer called %d times, want 1", *calls)
	}
	if strings.Join(*addrs, ",") != ":9300" {
		t.Fatalf("listened on %v, want [:9300]", *addrs)
	}
	waitClosed(t, ln.closed, "the upstream socket to close when Run returns")
}

func TestRun_DefaultsTheUpstreamAddr(t *testing.T) {
	t.Setenv("MOCK_UPSTREAM_ADDR", "")
	stubLogger(t, quietLogger())
	ln := newFakeListener()
	addrs := stubListen(t, ln, nil)
	stubRunServer(t)
	Run()
	if strings.Join(*addrs, ",") != ":9000" {
		t.Fatalf("listened on %v, want [:9000]", *addrs)
	}
	waitClosed(t, ln.closed, "the upstream socket to close when Run returns")
}

func TestRun_InvalidConfigIsFatal(t *testing.T) {
	t.Setenv("KEEL_LISTENERS_HTTP_PORT", "eighty")
	stubLogger(t, fatalLogger())
	stubListen(t, newFakeListener(), nil)
	calls := stubRunServer(t)
	expectFatal(t, func() { Run() })
	if *calls != 0 {
		t.Fatal("keel ran despite an invalid config")
	}
}

func TestRun_BindFailureIsFatal(t *testing.T) {
	stubLogger(t, fatalLogger())
	stubListen(t, nil, errors.New("address in use"))
	calls := stubRunServer(t)
	expectFatal(t, func() { Run() })
	if *calls != 0 {
		t.Fatal("keel ran without its upstream socket")
	}
}

func TestFirstRequest_IsTrueOncePerPath(t *testing.T) {
	u := newTestUpstream()
	if !u.firstRequest("/a") {
		t.Fatal("first /a: want true")
	}
	if u.firstRequest("/a") {
		t.Fatal("second /a: want false")
	}
	if !u.firstRequest("/b") {
		t.Fatal("first /b: want true")
	}
}

func TestCapture_AppendsTheRequestAndAnEndMarker(t *testing.T) {
	u := newTestUpstream()
	u.capture([]byte("GET / HTTP/1.1"))
	u.capture([]byte("GET /x HTTP/1.1"))
	want := "GET / HTTP/1.1\n--- end of request ---\nGET /x HTTP/1.1\n--- end of request ---\n"
	if got := u.captures.String(); got != want {
		t.Fatalf("captures = %q, want %q", got, want)
	}
}

func TestUp_BindsOnceAndAccepts(t *testing.T) {
	u := newTestUpstream()
	ln := newFakeListener()
	addrs := stubListen(t, ln, nil)
	if err := u.up(); err != nil {
		t.Fatal(err)
	}
	if err := u.up(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(*addrs, ",") != "fake:9000" {
		t.Fatalf("listened on %v, want one bind of fake:9000", *addrs)
	}
	u.down()
}

func TestUp_ReturnsTheListenError(t *testing.T) {
	u := newTestUpstream()
	stubListen(t, nil, errors.New("address in use"))
	if err := u.up(); err == nil || err.Error() != "address in use" {
		t.Fatalf("up() = %v, want the listen error", err)
	}
	if u.ln != nil {
		t.Fatal("a failed bind left a listener")
	}
}

func TestDown_ClosesTheSocketAndDropsConnections(t *testing.T) {
	u := newTestUpstream()
	ln := newFakeListener()
	stubListen(t, ln, nil)
	if err := u.up(); err != nil {
		t.Fatal(err)
	}
	server, client := net.Pipe()
	defer client.Close()
	u.track(server)
	u.down()
	waitClosed(t, ln.closed, "the socket to close")
	if u.ln != nil || len(u.conns) != 0 {
		t.Fatalf("after down: ln=%v conns=%d, want none", u.ln, len(u.conns))
	}
	if _, err := client.Write([]byte("x")); err == nil {
		t.Fatal("a tracked connection survived down")
	}
}

func TestDown_WhenAlreadyDownIsANoOp(t *testing.T) {
	u := newTestUpstream()
	u.down()
	if u.ln != nil {
		t.Fatal("down created a listener")
	}
}

func TestAccept_ServesEachConnectionInTheCurrentModeAndSkipsErrors(t *testing.T) {
	u := newTestUpstream()
	ln := newFakeListener()
	done := make(chan struct{})
	go func() { u.accept(ln); close(done) }()

	ln.accepts <- acceptResult{err: errors.New("transient accept failure")}
	server, client := net.Pipe()
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	ln.accepts <- acceptResult{conn: server}
	if _, err := io.WriteString(client, "GET /x HTTP/1.1\r\nHost: up\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(client)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != okResponse {
		t.Fatalf("response = %q, want %q", got, okResponse)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		u.mu.Lock()
		n := len(u.conns)
		u.mu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a served connection stayed tracked")
		}
		time.Sleep(time.Millisecond)
	}
	_ = ln.Close()
	waitClosed(t, done, "accept to return once the socket closed")
}

// controlRequest calls h with a request for method and target, the mode path value set.
func controlRequest(h http.HandlerFunc, method, target, mode string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	if mode != "" {
		r.SetPathValue("mode", mode)
	}
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func TestHandleMode_SwitchesModeAndResetsState(t *testing.T) {
	u := newTestUpstream()
	u.capture([]byte("old"))
	u.firstRequest("/a")
	server, client := net.Pipe()
	defer client.Close()
	u.track(server)
	w := controlRequest(u.handleMode, http.MethodPost, "/mode/hang", "hang")
	if w.Code != http.StatusNoContent {
		t.Fatalf("status %d, want 204", w.Code)
	}
	if u.mode != "hang" || u.captures.Len() != 0 || u.seen != nil || len(u.conns) != 0 {
		t.Fatalf("mode=%q captures=%d seen=%v conns=%d, want hang and all cleared",
			u.mode, u.captures.Len(), u.seen, len(u.conns))
	}
	if _, err := client.Write([]byte("x")); err == nil {
		t.Fatal("a connection from the old mode survived the switch")
	}
}

func TestHandleMode_RejectsAnUnknownMode(t *testing.T) {
	u := newTestUpstream()
	w := controlRequest(u.handleMode, http.MethodPost, "/mode/bogus", "bogus")
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "unknown mode: bogus") {
		t.Fatalf("status %d body %q, want 400 naming the mode", w.Code, w.Body.String())
	}
	if u.mode != modeOK {
		t.Fatalf("mode = %q, want it unchanged", u.mode)
	}
}

func TestHandleDown_ClosesTheSocket(t *testing.T) {
	u := newTestUpstream()
	ln := newFakeListener()
	stubListen(t, ln, nil)
	if err := u.up(); err != nil {
		t.Fatal(err)
	}
	if w := controlRequest(u.handleDown, http.MethodPost, "/down", ""); w.Code != http.StatusNoContent {
		t.Fatalf("status %d, want 204", w.Code)
	}
	waitClosed(t, ln.closed, "the socket to close")
}

func TestHandleUp_ReopensTheSocket(t *testing.T) {
	u := newTestUpstream()
	ln := newFakeListener()
	stubListen(t, ln, nil)
	if w := controlRequest(u.handleUp, http.MethodPost, "/up", ""); w.Code != http.StatusNoContent {
		t.Fatalf("status %d, want 204", w.Code)
	}
	if u.ln == nil {
		t.Fatal("up did not bind")
	}
	u.down()
}

func TestHandleUp_ReportsABindFailure(t *testing.T) {
	u := newTestUpstream()
	stubListen(t, nil, errors.New("address in use"))
	w := controlRequest(u.handleUp, http.MethodPost, "/up", "")
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "address in use") {
		t.Fatalf("status %d body %q, want 500 with the error", w.Code, w.Body.String())
	}
}

func TestHandleCaptures_ReturnsEveryCapturedRequest(t *testing.T) {
	u := newTestUpstream()
	u.capture([]byte("GET /x HTTP/1.1"))
	w := controlRequest(u.handleCaptures, http.MethodGet, "/captures", "")
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("status %d content-type %q", w.Code, w.Header().Get("Content-Type"))
	}
	if !bytes.Equal(w.Body.Bytes(), u.captures.Bytes()) {
		t.Fatalf("body %q, want the captures %q", w.Body.String(), u.captures.String())
	}
}

func TestTrack_RecordsTheConnectionAndReturnsTheMode(t *testing.T) {
	u := newTestUpstream()
	u.mode = "drip"
	server, client := net.Pipe()
	defer client.Close()
	defer server.Close()
	if got := u.track(server); got != "drip" {
		t.Fatalf("track = %q, want drip", got)
	}
	if _, ok := u.conns[server]; !ok {
		t.Fatal("connection not tracked")
	}
	u.untrack(server)
	if len(u.conns) != 0 {
		t.Fatal("untrack left the connection")
	}
}
