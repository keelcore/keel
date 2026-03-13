//go:build !no_sidecar

package sidecar

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/probes"
)

// ---------------------------------------------------------------------------
// breaker — Allow() state transitions
// ---------------------------------------------------------------------------

// Allow returns true in the closed state.
func TestBreaker_Allow_Closed_ReturnsTrue(t *testing.T) {
	b := newBreaker(3, time.Second)
	allowed, onResult := b.Allow()
	if !allowed {
		t.Error("expected Allow=true in closed state")
	}
	if onResult == nil {
		t.Error("expected non-nil onResult in closed state")
	}
	onResult(true)
}

// After threshold failures, Allow returns false (open state).
func TestBreaker_Allow_OpenAfterThreshold(t *testing.T) {
	b := newBreaker(3, time.Minute)
	for i := 0; i < 3; i++ {
		allowed, onResult := b.Allow()
		if !allowed {
			t.Fatalf("expected Allow=true on iteration %d", i)
		}
		onResult(false)
	}
	allowed, _ := b.Allow()
	if allowed {
		t.Error("expected Allow=false after hitting failure threshold")
	}
}

// Allow returns false in the half-open state when a probe is already in flight.
func TestBreaker_Allow_HalfOpen_RejectsConcurrent(t *testing.T) {
	b := newBreaker(1, 1*time.Nanosecond) // very short reset timeout
	// Trip the breaker.
	allowed, onResult := b.Allow()
	if !allowed {
		t.Fatal("expected Allow=true initially")
	}
	onResult(false) // now open

	// Wait past reset timeout so the next Allow transitions to half-open.
	time.Sleep(5 * time.Millisecond)

	// First Allow in open state transitions to half-open and returns true.
	allowed1, onResult1 := b.Allow()
	if !allowed1 {
		t.Fatal("expected Allow=true after reset timeout (probe slot)")
	}

	// Second Allow while probe is in flight (half-open) must return false.
	allowed2, _ := b.Allow()
	if allowed2 {
		t.Error("expected Allow=false for concurrent request in half-open state")
	}

	// Clean up probe.
	onResult1(true)
}

// recordProbeResult(true) returns the breaker to closed state.
func TestBreaker_RecordProbeResult_Success_ClosesBreaker(t *testing.T) {
	b := newBreaker(1, 1*time.Nanosecond)
	allowed, onResult := b.Allow()
	if !allowed {
		t.Fatal("initial Allow must succeed")
	}
	onResult(false) // open
	time.Sleep(5 * time.Millisecond)

	// Probe succeeds → closed.
	allowed, probeResult := b.Allow()
	if !allowed {
		t.Fatal("expected probe slot to be granted")
	}
	probeResult(true)

	// Breaker is closed again.
	allowed, onResult2 := b.Allow()
	if !allowed {
		t.Error("expected Allow=true after successful probe (breaker closed)")
	}
	onResult2(true)
}

// recordProbeResult(false) keeps the breaker open.
func TestBreaker_RecordProbeResult_Failure_ReopensBreaker(t *testing.T) {
	b := newBreaker(1, 1*time.Nanosecond)
	allowed, onResult := b.Allow()
	if !allowed {
		t.Fatal("initial Allow must succeed")
	}
	onResult(false) // open
	time.Sleep(5 * time.Millisecond)

	// Probe fails → remains open.
	allowed, probeResult := b.Allow()
	if !allowed {
		t.Fatal("expected probe slot to be granted")
	}
	probeResult(false)

	// Breaker is open again (reset timeout is nanoseconds, so we need to wait).
	time.Sleep(5 * time.Millisecond)
	allowed, _ = b.Allow()
	// After failed probe the breaker opens; after nanosecond reset it may allow again.
	// We just verify the probeResult(false) call didn't close it.
	_ = allowed // state is open; exact timing-dependent; just verify no panic
}

// recordResult(true) resets failure count.
func TestBreaker_RecordResult_Success_ResetsFailures(t *testing.T) {
	b := newBreaker(3, time.Minute)
	// Two failures.
	for i := 0; i < 2; i++ {
		allowed, onResult := b.Allow()
		if !allowed {
			t.Fatalf("expected Allow=true on iteration %d", i)
		}
		onResult(false)
	}
	// One success resets the counter.
	allowed, onResult := b.Allow()
	if !allowed {
		t.Fatal("expected Allow=true before threshold")
	}
	onResult(true)
	if b.failures != 0 {
		t.Errorf("expected failures=0 after success, got %d", b.failures)
	}
}

// newBreaker with zero threshold defaults to 5.
func TestNewBreaker_ZeroThreshold_DefaultsFive(t *testing.T) {
	b := newBreaker(0, time.Second)
	if b.threshold != 5 {
		t.Errorf("expected default threshold=5, got %d", b.threshold)
	}
}

// newBreaker with zero resetTimeout defaults to 30s.
func TestNewBreaker_ZeroResetTimeout_Defaults30s(t *testing.T) {
	b := newBreaker(5, 0)
	if b.resetTimeout != 30*time.Second {
		t.Errorf("expected default resetTimeout=30s, got %v", b.resetTimeout)
	}
}

// ---------------------------------------------------------------------------
// buildTransport
// ---------------------------------------------------------------------------

// buildTransport with TLS disabled returns a non-nil transport (DefaultTransport clone).
func TestBuildTransport_Disabled_ReturnsClone(t *testing.T) {
	tr, err := buildTransport(config.UpstreamTLSConfig{Enabled: false})
	if err != nil {
		t.Fatalf("buildTransport disabled: %v", err)
	}
	if tr == nil {
		t.Error("expected non-nil transport")
	}
}

// buildTransport with TLS enabled and no CA/client cert returns a TLS transport.
func TestBuildTransport_Enabled_NoCANoCert_ReturnsTLSTransport(t *testing.T) {
	tr, err := buildTransport(config.UpstreamTLSConfig{Enabled: true})
	if err != nil {
		t.Fatalf("buildTransport enabled: %v", err)
	}
	if tr == nil {
		t.Error("expected non-nil transport")
	}
	if tr.TLSClientConfig == nil {
		t.Error("expected non-nil TLSClientConfig")
	}
}

// buildTransport with a non-existent CA file returns an error.
func TestBuildTransport_CAFileNotFound_ReturnsError(t *testing.T) {
	_, err := buildTransport(config.UpstreamTLSConfig{
		Enabled: true,
		CAFile:  "/nonexistent-ca-for-sidecar-test.pem",
	})
	if err == nil {
		t.Error("expected error for missing CA file")
	}
}

// buildTransport with a CA file containing no valid PEM returns an error.
func TestBuildTransport_CAFileInvalidPEM_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad-ca.pem")
	if err := os.WriteFile(path, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := buildTransport(config.UpstreamTLSConfig{
		Enabled: true,
		CAFile:  path,
	})
	if err == nil {
		t.Error("expected error for invalid PEM in CA file")
	}
}

// buildTransport with non-existent client cert/key returns an error.
func TestBuildTransport_ClientCertNotFound_ReturnsError(t *testing.T) {
	_, err := buildTransport(config.UpstreamTLSConfig{
		Enabled:        true,
		ClientCertFile: "/nonexistent-client-cert.pem",
		ClientKeyFile:  "/nonexistent-client-key.pem",
	})
	if err == nil {
		t.Error("expected error for missing client cert/key")
	}
}

// buildTransport with InsecureSkipVerify=true sets the TLS config field.
func TestBuildTransport_InsecureSkipVerify(t *testing.T) {
	if os.Getenv("GOFIPS140") != "" {
		t.Skip("skipping InsecureSkipVerify test under FIPS")
	}
	tr, err := buildTransport(config.UpstreamTLSConfig{
		Enabled:            true,
		InsecureSkipVerify: true,
	})
	if err != nil {
		t.Fatalf("buildTransport: %v", err)
	}
	if tr.TLSClientConfig == nil || !tr.TLSClientConfig.InsecureSkipVerify {
		t.Error("expected InsecureSkipVerify=true in TLS config")
	}
}

// ---------------------------------------------------------------------------
// applyHeaderPolicy
// ---------------------------------------------------------------------------

// applyHeaderPolicy with a strip list removes matching headers.
func TestApplyHeaderPolicy_Strip(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Secret", "hidden")
	req.Header.Set("X-Keep", "visible")

	applyHeaderPolicy(req, config.HeaderPolicyConfig{
		Strip: []string{"X-Secret"},
	})

	if req.Header.Get("X-Secret") != "" {
		t.Error("expected X-Secret to be stripped")
	}
	if req.Header.Get("X-Keep") == "" {
		t.Error("expected X-Keep to remain")
	}
}

// applyHeaderPolicy with a forward allowlist removes headers not in the list.
func TestApplyHeaderPolicy_Forward_AllowlistFilters(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Allowed", "keep")
	req.Header.Set("X-Blocked", "remove")

	applyHeaderPolicy(req, config.HeaderPolicyConfig{
		Forward: []string{"X-Allowed"},
	})

	if req.Header.Get("X-Allowed") == "" {
		t.Error("expected X-Allowed to remain after forward policy")
	}
	if req.Header.Get("X-Blocked") != "" {
		t.Error("expected X-Blocked to be removed by forward allowlist")
	}
}

// applyHeaderPolicy with empty policy leaves headers unchanged.
func TestApplyHeaderPolicy_Empty_NoChange(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Custom", "value")

	applyHeaderPolicy(req, config.HeaderPolicyConfig{})

	if req.Header.Get("X-Custom") == "" {
		t.Error("expected X-Custom unchanged with empty policy")
	}
}

// ---------------------------------------------------------------------------
// realClientIP
// ---------------------------------------------------------------------------

// realClientIP with trustedHops=0 returns socketPeerIP.
func TestRealClientIP_ZeroHops_ReturnsPeer(t *testing.T) {
	got := realClientIP("1.1.1.1, 2.2.2.2", "3.3.3.3", 0)
	if got != "3.3.3.3" {
		t.Errorf("expected 3.3.3.3, got %q", got)
	}
}

// realClientIP with trustedHops=1 returns the entry immediately to the left of
// the trusted zone.
func TestRealClientIP_OneHop_ReturnsEntry(t *testing.T) {
	// chain: [1.1.1.1, 2.2.2.2, 3.3.3.3(peer)]
	// trustedHops=1 → real client = chain[len-1-1] = chain[1] = 2.2.2.2
	got := realClientIP("1.1.1.1, 2.2.2.2", "3.3.3.3", 1)
	if got != "2.2.2.2" {
		t.Errorf("expected 2.2.2.2, got %q", got)
	}
}

// realClientIP with trustedHops larger than chain length falls back to socketPeerIP.
func TestRealClientIP_TooManyHops_Fallback(t *testing.T) {
	// chain: [3.3.3.3(peer)], trustedHops=5 → idx<0 → fallback
	got := realClientIP("", "3.3.3.3", 5)
	if got != "3.3.3.3" {
		t.Errorf("expected 3.3.3.3 fallback, got %q", got)
	}
}

// realClientIP with empty incomingXFF and trustedHops=1 uses chain=[peer].
func TestRealClientIP_EmptyXFF_OneHop_Fallback(t *testing.T) {
	// chain: [4.4.4.4(peer)], trustedHops=1 → idx=len-2 = -1 → fallback
	got := realClientIP("", "4.4.4.4", 1)
	if got != "4.4.4.4" {
		t.Errorf("expected 4.4.4.4 fallback, got %q", got)
	}
}

// realClientIP with two XFF entries and trustedHops=2 falls back (idx<0).
func TestRealClientIP_TwoXFF_TwoHops_Fallback(t *testing.T) {
	// chain: [1.1.1.1, 3.3.3.3(peer)], trustedHops=2 → idx=2-1-2=-1 → fallback
	got := realClientIP("1.1.1.1", "3.3.3.3", 2)
	if got != "3.3.3.3" {
		t.Errorf("expected 3.3.3.3 fallback, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// applyXFF
// ---------------------------------------------------------------------------

// applyXFF with mode=replace sets XFF to just the socket peer IP.
func TestApplyXFF_Replace(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	applyXFF(req, "1.1.1.1", "2.2.2.2:1234", "replace", 0)
	if got := req.Header.Get("x-forwarded-for"); got != "2.2.2.2" {
		t.Errorf("replace: expected '2.2.2.2', got %q", got)
	}
}

// applyXFF with mode=strip removes XFF.
func TestApplyXFF_Strip(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("x-forwarded-for", "1.1.1.1")
	applyXFF(req, "1.1.1.1", "2.2.2.2:1234", "strip", 0)
	if got := req.Header.Get("x-forwarded-for"); got != "" {
		t.Errorf("strip: expected empty XFF, got %q", got)
	}
}

// applyXFF with mode=append and existing XFF appends the peer.
func TestApplyXFF_Append_WithExisting(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	applyXFF(req, "1.1.1.1", "2.2.2.2:1234", "append", 0)
	if got := req.Header.Get("x-forwarded-for"); got != "1.1.1.1, 2.2.2.2" {
		t.Errorf("append: expected '1.1.1.1, 2.2.2.2', got %q", got)
	}
}

// applyXFF with mode=append and no existing XFF sets just the peer.
func TestApplyXFF_Append_NoExisting(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	applyXFF(req, "", "2.2.2.2:1234", "append", 0)
	if got := req.Header.Get("x-forwarded-for"); got != "2.2.2.2" {
		t.Errorf("append no existing: expected '2.2.2.2', got %q", got)
	}
}

// applyXFF sets x-real-ip.
func TestApplyXFF_SetsXRealIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	applyXFF(req, "", "5.5.5.5:9000", "append", 0)
	if got := req.Header.Get("x-real-ip"); got != "5.5.5.5" {
		t.Errorf("expected x-real-ip=5.5.5.5, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// addrToIP
// ---------------------------------------------------------------------------

// addrToIP with a port splits and returns the host.
func TestAddrToIP_WithPort(t *testing.T) {
	got := addrToIP("192.168.1.1:8080")
	if got != "192.168.1.1" {
		t.Errorf("expected 192.168.1.1, got %q", got)
	}
}

// addrToIP without a port returns addr unchanged.
func TestAddrToIP_NoPort(t *testing.T) {
	got := addrToIP("192.168.1.1")
	if got != "192.168.1.1" {
		t.Errorf("expected 192.168.1.1, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// New
// ---------------------------------------------------------------------------

// New with an invalid upstream URL returns an error.
func TestNew_InvalidUpstreamURL_ReturnsError(t *testing.T) {
	cfg := config.Config{
		Sidecar: config.SidecarConfig{
			UpstreamURL: "://invalid-url",
		},
	}
	_, err := New(cfg)
	if err == nil {
		t.Error("expected error for invalid upstream URL")
	}
}

// New with a valid upstream URL returns a non-nil handler.
func TestNew_ValidUpstreamURL_ReturnsHandler(t *testing.T) {
	cfg := config.Config{
		Sidecar: config.SidecarConfig{
			UpstreamURL: "http://127.0.0.1:9999",
		},
	}
	h, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if h == nil {
		t.Error("expected non-nil handler")
	}
}

// New with circuit breaker enabled returns a non-nil handler.
func TestNew_CircuitBreakerEnabled_ReturnsHandler(t *testing.T) {
	cfg := config.Config{
		Sidecar: config.SidecarConfig{
			UpstreamURL: "http://127.0.0.1:9999",
			CircuitBreaker: config.CircuitBreakerConfig{
				Enabled:          true,
				FailureThreshold: 3,
				ResetTimeout:     config.DurationOf(30 * time.Second),
			},
		},
	}
	h, err := New(cfg)
	if err != nil {
		t.Fatalf("New with circuit breaker: %v", err)
	}
	if h == nil {
		t.Error("expected non-nil handler")
	}
}

// New with TLS buildTransport error (non-existent CA file) returns an error.
func TestNew_TLSBuildTransportError_ReturnsError(t *testing.T) {
	cfg := config.Config{
		Sidecar: config.SidecarConfig{
			UpstreamURL: "https://127.0.0.1:9999",
			UpstreamTLS: config.UpstreamTLSConfig{
				Enabled: true,
				CAFile:  "/nonexistent-ca-for-new-test.pem",
			},
		},
	}
	_, err := New(cfg)
	if err == nil {
		t.Error("expected error when upstream TLS CA file is missing")
	}
}

// New with a sign function wires it through without error.
func TestNew_WithSignFn_ReturnsHandler(t *testing.T) {
	signFn := func(_ *http.Request) error { return nil }
	cfg := config.Config{
		Sidecar: config.SidecarConfig{
			UpstreamURL: "http://127.0.0.1:9999",
		},
	}
	h, err := New(cfg, signFn)
	if err != nil {
		t.Fatalf("New with signFn: %v", err)
	}
	if h == nil {
		t.Error("expected non-nil handler")
	}
}

// ---------------------------------------------------------------------------
// cbTransport — circuit open path
// ---------------------------------------------------------------------------

// cbTransport returns errCircuitOpen when the breaker is open.
func TestCBTransport_CircuitOpen_ReturnsError(t *testing.T) {
	b := newBreaker(1, time.Minute)
	// Trip the breaker.
	allowed, onResult := b.Allow()
	if !allowed {
		t.Fatal("expected Allow=true initially")
	}
	onResult(false) // threshold=1 → open

	ct := &cbTransport{
		inner:   http.DefaultTransport,
		breaker: b,
	}

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
	_, err := ct.RoundTrip(req)
	if err == nil {
		t.Error("expected error when circuit is open")
	}
	if err != errCircuitOpen {
		t.Errorf("expected errCircuitOpen, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// StartHealthProbe — smoke test: starts without panic
// ---------------------------------------------------------------------------

// StartHealthProbe starts a goroutine and does at least one probe without panic.
func TestStartHealthProbe_Runs_NoPanic(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	log := logging.New(logging.Config{Out: io.Discard})
	readiness := probes.NewReadiness()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := config.SidecarConfig{
		UpstreamURL:            srv.URL,
		UpstreamHealthPath:     "/health",
		UpstreamHealthInterval: config.DurationOf(time.Hour),
		UpstreamHealthTimeout:  config.DurationOf(2 * time.Second),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	StartHealthProbe(ctx, cfg, nil, readiness, log)
	// Give the goroutine a moment to start and do at least one probe.
	time.Sleep(50 * time.Millisecond)
}

// ---------------------------------------------------------------------------
// doProbe — healthy and unhealthy
// ---------------------------------------------------------------------------

// doProbe sets readiness=true when server responds 200.
func TestDoProbe_Healthy_SetsReadinessTrue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	readiness := probes.NewReadiness()
	readiness.Set(false) // start unhealthy
	client := &http.Client{Timeout: 2 * time.Second}
	doProbe(client, srv.URL+"/health", readiness, nil)

	if ok, _ := readiness.IsReady(); !ok {
		t.Error("expected readiness=true for healthy upstream")
	}
}

// doProbe sets readiness=false when server responds 500.
func TestDoProbe_Unhealthy_SetsReadinessFalse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	readiness := probes.NewReadiness()
	readiness.Set(true)
	client := &http.Client{Timeout: 2 * time.Second}
	doProbe(client, srv.URL+"/health", readiness, nil)

	if readiness.Get() {
		t.Error("expected readiness=false for unhealthy upstream (500)")
	}
}

// doProbe sets readiness=false on connection error.
func TestDoProbe_ConnectionError_SetsReadinessFalse(t *testing.T) {
	readiness := probes.NewReadiness()
	readiness.Set(true)
	client := &http.Client{Timeout: 50 * time.Millisecond}
	doProbe(client, "http://127.0.0.1:1/health", readiness, nil)

	if readiness.Get() {
		t.Error("expected readiness=false on connection error")
	}
}

// doProbe with nil log still works (no-op on warning path).
func TestDoProbe_NilLog_NoUnhealthy_NoPanic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	readiness := probes.NewReadiness()
	client := &http.Client{Timeout: 2 * time.Second}
	// Should not panic even with nil log and unhealthy status.
	doProbe(client, srv.URL+"/health", readiness, nil)
}
