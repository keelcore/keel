//go:build !no_sidecar

package sidecar

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/keelcore/keel/pkg/config"
)

var (
	errCircuitOpen      = errors.New("circuit breaker open")
	errResponseTooLarge = errors.New("upstream response exceeded max_response_body_bytes")
	errSignFailed       = errors.New("outbound request signing failed")
)

// Seam for injecting library values in tests. The production value is the bare
// library value, so runtime behaviour is unchanged.
var httpDefaultTransport http.RoundTripper = http.DefaultTransport

// limitBody streams an upstream body while capping it at remaining bytes. It
// never buffers: each Read passes through at most the bytes still allowed, and
// once the cap is reached a one-byte probe distinguishes a body that ends
// exactly at the cap (EOF) from one that exceeds it (errResponseTooLarge).
type limitBody struct {
	rc        io.ReadCloser
	remaining int64
}

func (b *limitBody) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		var probe [1]byte
		n, err := b.rc.Read(probe[:])
		if n > 0 {
			return 0, errResponseTooLarge
		}
		return 0, err
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.rc.Read(p)
	b.remaining -= int64(n)
	return n, err
}

func (b *limitBody) Close() error { return b.rc.Close() }

// New creates a configured sidecar reverse proxy.
// It applies XFF policy, header allowlist/denylist, response size capping,
// upstream TLS (including mTLS), and an optional circuit breaker.
// The optional sign function is called on each outbound request after header
// policy is applied (e.g. to attach a keel-to-keel Authorization JWT).
func New(cfg config.Config, sign ...func(*http.Request) error) (http.Handler, error) {
	u, err := url.Parse(upstreamBase(cfg.Sidecar.UpstreamURL))
	if err != nil {
		return nil, fmt.Errorf("parse upstream_url: %w", err)
	}

	transport, err := upstreamTransport(cfg.Sidecar, upstreamHeaderBudget(cfg.Timeouts.Write.Duration))
	if err != nil {
		return nil, fmt.Errorf("build upstream transport: %w", err)
	}

	var rt http.RoundTripper = transport
	if cfg.Sidecar.CircuitBreaker.Enabled {
		cb := newBreaker(
			cfg.Sidecar.CircuitBreaker.FailureThreshold,
			cfg.Sidecar.CircuitBreaker.ResetTimeout.Duration,
		)
		rt = &cbTransport{inner: transport, breaker: cb}
	}

	maxResp := cfg.Security.MaxResponseBodyBytes
	xffMode := cfg.Sidecar.XFFMode
	xffHops := cfg.Sidecar.XFFTrustedHops
	policy := cfg.Sidecar.HeaderPolicy

	// The sign function runs in a transport wrapper outside the breaker, so a
	// signing failure aborts the request with 502 (never forwarding unsigned)
	// and does not count as an upstream failure.
	signing := len(sign) > 0 && sign[0] != nil
	if signing {
		rt = &signTransport{inner: rt, sign: sign[0]}
	}

	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(u)
			// Read existing XFF from the inbound request before the proxy may
			// strip it, then apply the configured mode to the outbound request.
			incomingXFF := pr.In.Header.Get("x-forwarded-for")
			applyXFF(pr.Out, incomingXFF, pr.In.RemoteAddr, xffMode, xffHops)
			applyHeaderPolicy(pr.Out, policy)
			// When keel signs outbound requests the client's own credentials are
			// never forwarded; signTransport attaches keel's token.
			if signing {
				pr.Out.Header.Del("Authorization")
			}
		},
		Transport: rt,
		// Responses stream through the proxy's fixed copy buffer — never buffered
		// whole — so a slow client applies TCP backpressure to the upstream read.
		// The size cap is enforced up front on a declared Content-Length (clean
		// 502) and otherwise while streaming: past the cap the copy fails and the
		// proxy aborts the client connection (headers are already sent).
		ModifyResponse: func(resp *http.Response) error {
			if maxResp <= 0 {
				return nil
			}
			if resp.ContentLength > maxResp {
				resp.Body.Close()
				return errResponseTooLarge
			}
			resp.Body = &limitBody{rc: resp.Body, remaining: maxResp}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, handlerErr error) {
			switch {
			case errors.Is(handlerErr, errCircuitOpen):
				http.Error(w, "circuit breaker open", http.StatusBadGateway)
			case errors.Is(handlerErr, errResponseTooLarge):
				http.Error(w, "upstream response too large", http.StatusBadGateway)
			default:
				http.Error(w, "bad gateway", http.StatusBadGateway)
			}
		},
	}

	return rp, nil
}

// buildTransport constructs an *http.Transport for upstream connections. Both
// branches start from a clone of http.DefaultTransport (dial, TLS handshake and
// idle timeouts). When cfg.Enabled is true, a TLS 1.3-minimum client config is
// added with optional CA pool, client cert (mTLS), and insecure-skip-verify.
func buildTransport(cfg config.UpstreamTLSConfig) (*http.Transport, error) {
	t := &http.Transport{}
	if dt, ok := httpDefaultTransport.(*http.Transport); ok {
		t = dt.Clone()
	}
	if !cfg.Enabled {
		return t, nil
	}
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS13, //nolint:gosec // TLS 1.3 minimum enforced
	}

	if cfg.InsecureSkipVerify {
		tlsCfg.InsecureSkipVerify = true //nolint:gosec // operator-configured
	}

	if cfg.CAFile != "" {
		pemData, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read ca_file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pemData) {
			return nil, fmt.Errorf("parse ca_file %q: no certificates found", cfg.CAFile)
		}
		tlsCfg.RootCAs = pool
	}

	if cfg.ClientCertFile != "" && cfg.ClientKeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.ClientCertFile, cfg.ClientKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load client cert: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	t.TLSClientConfig = tlsCfg
	return t, nil
}

// applyHeaderPolicy strips denylist headers. If the forward allowlist is
// non-empty, any header not in the allowlist is also removed.
func applyHeaderPolicy(req *http.Request, policy config.HeaderPolicyConfig) {
	for _, h := range policy.Strip {
		req.Header.Del(h)
	}
	if len(policy.Forward) == 0 {
		return
	}
	allowed := make(map[string]struct{}, len(policy.Forward))
	for _, h := range policy.Forward {
		allowed[http.CanonicalHeaderKey(h)] = struct{}{}
	}
	for k := range req.Header {
		if _, ok := allowed[k]; !ok {
			delete(req.Header, k)
		}
	}
}

// cbTransport wraps a RoundTripper with circuit breaker gating.
type cbTransport struct {
	inner   http.RoundTripper
	breaker *breaker
}

func (t *cbTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	allowed, probe, onResult := t.breaker.allow()
	if !allowed {
		return nil, errCircuitOpen
	}
	resp, err := t.inner.RoundTrip(req)
	if err != nil {
		// The inbound client cancelling or timing out says nothing about upstream
		// health; counting it would let any client open the breaker for everyone.
		if req.Context().Err() != nil {
			if probe {
				t.breaker.abandonProbe()
			}
			return nil, err
		}
		onResult(false)
		return nil, err
	}
	onResult(resp.StatusCode < 500)
	return resp, nil
}

// signTransport attaches keel's outbound credentials to each upstream request.
// It signs a clone, per the RoundTripper contract, and fails the round trip
// (errSignFailed → 502) rather than forwarding the request unsigned.
type signTransport struct {
	inner http.RoundTripper
	sign  func(*http.Request) error
}

func (t *signTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	out := req.Clone(req.Context())
	if err := t.sign(out); err != nil {
		return nil, fmt.Errorf("%w: %w", errSignFailed, err)
	}
	return t.inner.RoundTrip(out)
}

// unlimitedIdle lifts net/http's per-host idle cap (default 2). Idle
// connections only exist up to peak concurrency and IdleConnTimeout reaps them,
// so concurrency is bounded by limits.max_concurrent, the memory guard and the
// file-descriptor limit — not by connection churn.
const unlimitedIdle = math.MaxInt32

// upstreamTransport tunes buildTransport for a sidecar's single upstream:
// connection reuse without caps, HTTP/2 where the upstream offers it (ALPN over
// TLS, or h2c when configured), no environment proxy, a response-header
// deadline so a hung upstream frees its connection, and unix-socket dialing for
// a unix:// upstream_url.
func upstreamTransport(sc config.SidecarConfig, responseHeaderTimeout time.Duration) (*http.Transport, error) {
	t, err := buildTransport(sc.UpstreamTLS)
	if err != nil {
		return nil, err
	}
	t.Proxy = nil
	t.MaxConnsPerHost = 0
	t.MaxIdleConns = 0
	t.MaxIdleConnsPerHost = unlimitedIdle
	t.ResponseHeaderTimeout = responseHeaderTimeout
	t.ForceAttemptHTTP2 = true
	if sc.UpstreamH2C {
		t.Protocols = new(http.Protocols)
		t.Protocols.SetUnencryptedHTTP2(true)
	}
	if socket, ok := strings.CutPrefix(sc.UpstreamURL, "unix://"); ok {
		var d net.Dialer
		t.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return d.DialContext(ctx, "unix", socket)
		}
	}
	return t, nil
}

// upstreamHeaderBudget is the upstream response-header deadline: 90% of the
// server's write timeout, which also runs while keel waits on the upstream.
// Equal deadlines race, and when the server's fires first the 502 is never
// written; the remaining 10% is left to write it.
func upstreamHeaderBudget(write time.Duration) time.Duration {
	return write - write/10
}

// upstreamBase is the URL requests are addressed to: upstream_url itself, or
// http://localhost for a unix:// upstream (the socket is dialed instead).
func upstreamBase(upstreamURL string) string {
	if strings.HasPrefix(upstreamURL, "unix://") {
		return "http://localhost"
	}
	return upstreamURL
}
