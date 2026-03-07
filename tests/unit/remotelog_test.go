//go:build !no_remotelog

package unit

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/core/logging"
)

func TestRemoteLog_HTTPSink_ReceivesLogLines(t *testing.T) {
	var mu sync.Mutex
	var received []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		received = append(received, b...)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sink := logging.NewHTTPSink(srv.URL, 100, 10*time.Millisecond)
	_, _ = sink.Write([]byte(`{"msg":"hello"}` + "\n"))

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go sink.Run(ctx)
	<-ctx.Done()
	time.Sleep(10 * time.Millisecond) // let final flush complete

	mu.Lock()
	body := string(received)
	mu.Unlock()

	if !strings.Contains(body, "hello") {
		t.Errorf("expected log line in HTTP sink, got: %q", body)
	}
}

func TestRemoteLog_HTTPSink_DropsWhenFull(t *testing.T) {
	// bufCap=2: third write should be dropped.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sink := logging.NewHTTPSink(srv.URL, 2, time.Hour) // huge flush interval → no auto-flush
	_, _ = sink.Write([]byte("line1\n"))
	_, _ = sink.Write([]byte("line2\n"))
	_, _ = sink.Write([]byte("line3\n")) // should be dropped

	if drops := sink.DropsTotal(); drops != 1 {
		t.Errorf("expected 1 drop, got %d", drops)
	}
}

func TestRemoteLog_HTTPSink_AttachesToLogger(t *testing.T) {
	// Verify that an HTTPSink used as an io.Writer receives logger output.
	var mu sync.Mutex
	var received []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		received = append(received, b...)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sink := logging.NewHTTPSink(srv.URL, 100, 10*time.Millisecond)
	log := logging.New(logging.Config{Out: sink})
	log.Info("test-event", nil)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go sink.Run(ctx)
	<-ctx.Done()
	time.Sleep(10 * time.Millisecond)

	mu.Lock()
	body := string(received)
	mu.Unlock()

	if !strings.Contains(body, "test-event") {
		t.Errorf("expected test-event in sink, got: %q", body)
	}
}