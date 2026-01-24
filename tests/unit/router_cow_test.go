package unit

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/core/router"
)

func TestRouter_LastWriteWins_COW_InFlightPreserved_SamePort(t *testing.T) {
	const port = 8080

	r := router.New()

	var old_started atomic.Bool
	var allow_old_finish atomic.Bool

	oldH := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		old_started.Store(true)
		for !allow_old_finish.Load() {
			time.Sleep(2 * time.Millisecond)
		}
		_, _ = w.Write([]byte("old"))
	})

	newH := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("new"))
	})

	// Initial route on SAME port/pattern.
	r.Handle(port, "/x", oldH)

	h := r.Handler()

	// Start an in-flight request that must bind to OLD handler.
	req1 := httptest.NewRequest("GET", "http://example.com/x", nil)
	req1.Host = "example.com:8080"
	rr1 := httptest.NewRecorder()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		h.ServeHTTP(rr1, req1)
	}()

	// Wait until old handler is definitely running.
	deadline := time.Now().Add(750 * time.Millisecond)
	for !old_started.Load() && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if !old_started.Load() {
		t.Fatal("old handler never started")
	}

	// Re-register SAME (port, pattern) => NEW handler for NEW requests.
	r.Handle(port, "/x", newH)

	// New request should see NEW immediately.
	req2 := httptest.NewRequest("GET", "http://example.com/x", nil)
	req2.Host = "example.com:8080"
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req2)

	b2, _ := io.ReadAll(rr2.Result().Body)
	got2 := string(b2)
	if rr2.Code != 200 {
		t.Fatalf("expected 200 for new request, got %d body=%q", rr2.Code, got2)
	}
	if !strings.Contains(got2, "new") {
		t.Fatalf("expected new handler for second request, got %q", got2)
	}

	// Allow the old request to finish; it must still return OLD.
	allow_old_finish.Store(true)
	wg.Wait()

	b1, _ := io.ReadAll(rr1.Result().Body)
	got1 := string(b1)
	if rr1.Code != 200 {
		t.Fatalf("expected 200 for in-flight request, got %d body=%q", rr1.Code, got1)
	}
	if !strings.Contains(got1, "old") {
		t.Fatalf("expected old handler to complete for in-flight request, got %q", got1)
	}
}
