package core

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
)

// backpressure.* is reloadable: a second applyBackpressure stops the running
// loops (no two loops write the same flag), resets the backpressure flag, and
// starts loops with the new values.
func TestApplyBackpressure_RestartsLoopsOnReload(t *testing.T) {
	s := NewServer(logging.New(logging.Config{Out: io.Discard}), config.Config{})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s.runCtx = ctx

	var cfg config.Config
	cfg.Backpressure.SheddingEnabled = true
	cfg.Backpressure.HeapMaxBytes = 1 << 40
	cfg.Backpressure.MinHeadroomBytes = 10 << 20
	s.applyBackpressure(cfg)
	first := s.bpCancel

	s.readiness.Set(false) // as if the old pressure loop had latched
	cfg.Backpressure.SheddingEnabled = false
	s.applyBackpressure(cfg)
	if s.bpCancel == nil || &first == &s.bpCancel {
		t.Fatal("loops were not restarted")
	}
	if !s.readiness.Get() {
		t.Fatal("backpressure flag left latched across reload")
	}
	cancel()
	done := make(chan struct{})
	go func() { s.bpWg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("backpressure loops did not stop")
	}
}

// authn.trusted_ids is reloadable: wrapMain reads the allowlist from the authn
// snapshot per request, falling back to the startup list before one exists.
func TestWrapMain_TrustedIDsReload(t *testing.T) {
	if os.Getenv("GOFIPS140") != "" {
		t.Skip("HMAC fixtures not used under FIPS 140-only mode")
	}
	const secret = "reload-test-hmac-secret-0123456789abcdef"
	var cfg config.Config
	cfg.Authn.Enabled = true
	cfg.Authn.TrustedSigners = []string{secret}
	cfg.Authn.TrustedIDs = []string{"alice"}
	s := NewServer(logging.New(logging.Config{Out: io.Discard}), cfg)
	h := s.wrapMain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256,
		jwt.MapClaims{"sub": "bob", "exp": time.Now().Add(time.Minute).Unix()}).SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	call := func() int {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	s.applyAuthnState(cfg) // startup snapshot: alice only
	if code := call(); code != http.StatusForbidden {
		t.Fatalf("bob before reload: status %d, want 403", code)
	}
	cfg.Authn.TrustedIDs = []string{"bob"}
	s.applyAuthnState(cfg) // reload adds bob
	if code := call(); code != http.StatusOK {
		t.Fatalf("bob after reload: status %d, want 200", code)
	}
}

func TestAuthnSnapshot_TrustedIDSetNilSafe(t *testing.T) {
	var sn *authnSnapshot
	if sn.trustedIDSet() != nil {
		t.Fatal("nil snapshot returned a non-nil set")
	}
	set := map[string]struct{}{"alice": {}}
	if got := (&authnSnapshot{trustedIDs: set}).trustedIDSet(); len(got) != 1 {
		t.Fatalf("got %v", got)
	}
}

// Reload before Run with a remote sink configured must not panic on the nil
// run context; the sink is attached when Run applies the config.
func TestApplyRemoteSink_BeforeRunDoesNotPanic(t *testing.T) {
	s := NewServer(logging.New(logging.Config{Out: io.Discard}), config.Config{})
	var cfg config.Config
	cfg.Logging.RemoteSink.Enabled = true
	cfg.Logging.RemoteSink.Protocol = "http"
	cfg.Logging.RemoteSink.Endpoint = "http://127.0.0.1:1/logs"
	s.applyRemoteSink(cfg)
	if s.sinkOut != nil || s.sinkCancel != nil {
		t.Fatal("sink attached before Run")
	}
}
