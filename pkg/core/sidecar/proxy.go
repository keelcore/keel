//go:build !no_sidecar

package sidecar

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"

	"github.com/keelcore/keel/pkg/config"
)

var (
	errCircuitOpen      = errors.New("circuit breaker open")
	errResponseTooLarge = errors.New("upstream response exceeded max_response_body_bytes")
)

// New creates a configured sidecar reverse proxy.
// It applies XFF policy, header allowlist/denylist, response size capping,
// upstream TLS (including mTLS), and an optional circuit breaker.
func New(cfg config.Config) (http.Handler, error) {
	u, err := url.Parse(cfg.Sidecar.UpstreamURL)
	if err != nil {
		return nil, fmt.Errorf("parse upstream_url: %w", err)
	}

	transport, err := buildTransport(cfg.Sidecar.UpstreamTLS)
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

	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(u)
			// Read existing XFF from the inbound request before the proxy may
			// strip it, then apply the configured mode to the outbound request.
			incomingXFF := pr.In.Header.Get("x-forwarded-for")
			applyXFF(pr.Out, incomingXFF, pr.In.RemoteAddr, xffMode, xffHops)
			applyHeaderPolicy(pr.Out, policy)
		},
		Transport: rt,
		ModifyResponse: func(resp *http.Response) error {
			if maxResp <= 0 {
				return nil
			}
			limited := io.LimitReader(resp.Body, maxResp+1)
			buf, readErr := io.ReadAll(limited)
			resp.Body.Close()
			if readErr != nil {
				return fmt.Errorf("read upstream body: %w", readErr)
			}
			if int64(len(buf)) > maxResp {
				return errResponseTooLarge
			}
			resp.Body = io.NopCloser(bytes.NewReader(buf))
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

// buildTransport constructs an *http.Transport for upstream connections.
// When cfg.Enabled is false, a clone of http.DefaultTransport is returned.
// When cfg.Enabled is true, a TLS 1.3-minimum transport is built with optional
// CA pool, client cert (mTLS), and insecure-skip-verify.
func buildTransport(cfg config.UpstreamTLSConfig) (*http.Transport, error) {
	if !cfg.Enabled {
		if dt, ok := http.DefaultTransport.(*http.Transport); ok {
			return dt.Clone(), nil
		}
		return &http.Transport{}, nil
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

	return &http.Transport{TLSClientConfig: tlsCfg}, nil
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
	allowed, onResult := t.breaker.Allow()
	if !allowed {
		return nil, errCircuitOpen
	}
	resp, err := t.inner.RoundTrip(req)
	if err != nil {
		onResult(false)
		return nil, err
	}
	onResult(resp.StatusCode < 500)
	return resp, nil
}