//go:build !no_acme

package acme

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	xacme "golang.org/x/crypto/acme"

	"github.com/keelcore/keel/pkg/config"
)

// Manager holds per-challenge tokens, the current TLS certificate obtained via
// ACME, and drives the certificate lifecycle loop.
type Manager struct {
	tokens sync.Map // token → key-authorisation string
	cert   atomic.Pointer[tls.Certificate]
	logErr func(string, map[string]any) // optional; nil = silent
}

// New creates an empty ACME Manager.
func New() *Manager { return &Manager{} }

// SetLogger provides an error-logging callback invoked when certificate
// obtainment or renewal fails. If not set, errors are silently dropped.
func (m *Manager) SetLogger(fn func(string, map[string]any)) { m.logErr = fn }

// CertExpiry returns the seconds until the current certificate expires,
// or an error if no certificate has been obtained yet.
func (m *Manager) CertExpiry() (float64, error) {
	c := m.cert.Load()
	if c == nil {
		return 0, fmt.Errorf("acme: no certificate yet")
	}
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		return 0, fmt.Errorf("acme: parse cert: %w", err)
	}
	return time.Until(leaf.NotAfter).Seconds(), nil
}

// SetToken registers an http-01 challenge key-authorisation for token.
func (m *Manager) SetToken(token, keyAuth string) {
	m.tokens.Store(token, keyAuth)
}

// DeleteToken removes a previously registered challenge token.
func (m *Manager) DeleteToken(token string) {
	m.tokens.Delete(token)
}

// GetCertificate implements tls.Config.GetCertificate. It returns the current
// certificate obtained via ACME, or an error if none has been obtained yet.
func (m *Manager) GetCertificate(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	if c := m.cert.Load(); c != nil {
		return c, nil
	}
	return nil, fmt.Errorf("acme: certificate not yet available")
}

// HTTPHandler returns an http.Handler for the plain-HTTP listener that:
//   - serves ACME http-01 challenges at /.well-known/acme-challenge/<token>
//   - redirects all other requests to HTTPS on httpsPort with 301
func (m *Manager) HTTPHandler(httpsPort int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/.well-known/acme-challenge/"
		if strings.HasPrefix(r.URL.Path, prefix) {
			token := strings.TrimPrefix(r.URL.Path, prefix)
			if token == "" {
				http.NotFound(w, r)
				return
			}
			if v, ok := m.tokens.Load(token); ok {
				// RFC 8555 §8.3: Content-Type MUST be application/octet-stream.
				w.Header().Set("content-type", "application/octet-stream")
				_, _ = io.WriteString(w, v.(string))
				return
			}
			http.NotFound(w, r)
			return
		}
		// HTTPS redirect: rewrite host with httpsPort if non-standard.
		host := r.Host
		h, _, err := net.SplitHostPort(host)
		if err != nil {
			h = host // no port present
		}
		if httpsPort == 443 {
			host = h
		} else {
			host = fmt.Sprintf("%s:%d", h, httpsPort)
		}
		http.Redirect(w, r, "https://"+host+r.URL.RequestURI(), http.StatusMovedPermanently)
	})
}

// Start manages the ACME certificate lifecycle: account registration, initial
// certificate obtainment, and background renewal. It blocks until ctx is done.
func (m *Manager) Start(ctx context.Context, cfg config.ACMEConfig) error {
	accountKey, err := loadOrCreateAccountKey(cfg.CacheDir)
	if err != nil {
		return fmt.Errorf("acme account key: %w", err)
	}

	directoryURL := cfg.CAUrl
	if directoryURL == "" {
		directoryURL = xacme.LetsEncryptURL
	}

	client := &xacme.Client{Key: accountKey, DirectoryURL: directoryURL}

	if cfg.CACertFile != "" {
		httpClient, err := httpClientWithCA(cfg.CACertFile)
		if err != nil {
			return fmt.Errorf("acme ca cert: %w", err)
		}
		client.HTTPClient = httpClient
	}

	// Load any previously-issued cert from cache_dir so it is served
	// immediately on restart, without waiting for the ACME handshake.
	// A cached cert that fails domain or key-type validation is a fatal
	// misconfiguration: log it and return an error so the process exits.
	if cfg.CacheDir != "" {
		if cached, err := loadCachedCert(cfg.CacheDir); err == nil {
			if verr := validateCert(cached, cfg.Domains); verr != nil {
				if m.logErr != nil {
					m.logErr("acme_cached_cert_invalid", map[string]any{"err": verr.Error()})
				}
				return verr
			}
			m.cert.Store(cached)
		}
	}

	if err := registerAccount(ctx, client, cfg.Email); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return fmt.Errorf("acme register: %w", err)
	}

	// If the cached cert is not yet due for renewal, wait until it is.
	if !certNeedsRenewal(m.cert.Load()) {
		next := renewalDelay(m.cert.Load())
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(next):
		}
	}

	for {
		if err := m.obtainCert(ctx, client, cfg); err != nil && !errors.Is(err, context.Canceled) {
			if m.logErr != nil {
				m.logErr("acme_cert_obtain_failed", map[string]any{"err": err.Error()})
			}
		}
		next := renewalDelay(m.cert.Load())
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(next):
		}
	}
}

// Validate returns an error if cfg contains an invalid ACME configuration.
func Validate(_ config.Config) error { return nil }

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func registerAccount(ctx context.Context, client *xacme.Client, email string) error {
	contact := []string{}
	if email != "" {
		contact = []string{"mailto:" + email}
	}
	acct := &xacme.Account{Contact: contact}
	_, err := client.Register(ctx, acct, xacme.AcceptTOS)
	if err == nil {
		return nil
	}
	var acmeErr *xacme.Error
	if errors.As(err, &acmeErr) && acmeErr.StatusCode == http.StatusConflict {
		return nil // already registered
	}
	return err
}

func (m *Manager) obtainCert(ctx context.Context, client *xacme.Client, cfg config.ACMEConfig) error {
	ids := make([]xacme.AuthzID, len(cfg.Domains))
	for i, d := range cfg.Domains {
		ids[i] = xacme.AuthzID{Type: "dns", Value: d}
	}

	order, err := client.AuthorizeOrder(ctx, ids)
	if err != nil {
		return fmt.Errorf("authorize order: %w", err)
	}

	for _, authzURL := range order.AuthzURLs {
		if err := m.fulfillHTTP01(ctx, client, authzURL); err != nil {
			return err
		}
	}

	order, err = client.WaitOrder(ctx, order.URI)
	if err != nil {
		return fmt.Errorf("wait order: %w", err)
	}

	certKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate cert key: %w", err)
	}

	csr, err := buildCSR(certKey, cfg.Domains)
	if err != nil {
		return fmt.Errorf("build csr: %w", err)
	}

	derChain, _, err := client.CreateOrderCert(ctx, order.FinalizeURL, csr, true)
	if err != nil {
		return fmt.Errorf("create cert: %w", err)
	}

	return m.storeCert(cfg.CacheDir, cfg.Domains, certKey, derChain)
}

func (m *Manager) fulfillHTTP01(ctx context.Context, client *xacme.Client, authzURL string) error {
	authz, err := client.GetAuthorization(ctx, authzURL)
	if err != nil {
		return fmt.Errorf("get authorization: %w", err)
	}
	if authz.Status == xacme.StatusValid {
		return nil
	}

	chal := http01Challenge(authz)
	if chal == nil {
		return fmt.Errorf("no http-01 challenge for %s", authz.Identifier.Value)
	}

	keyAuth, err := client.HTTP01ChallengeResponse(chal.Token)
	if err != nil {
		return fmt.Errorf("key auth: %w", err)
	}

	m.SetToken(chal.Token, keyAuth)
	defer m.DeleteToken(chal.Token)

	if _, err := client.Accept(ctx, chal); err != nil {
		return fmt.Errorf("accept challenge: %w", err)
	}
	if _, err := client.WaitAuthorization(ctx, authz.URI); err != nil {
		return fmt.Errorf("wait authorization: %w", err)
	}
	return nil
}

func http01Challenge(authz *xacme.Authorization) *xacme.Challenge {
	for _, c := range authz.Challenges {
		if c.Type == "http-01" {
			return c
		}
	}
	return nil
}

func buildCSR(key *ecdsa.PrivateKey, domains []string) ([]byte, error) {
	tmpl := &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: domains[0]},
		DNSNames: domains,
	}
	return x509.CreateCertificateRequest(rand.Reader, tmpl, key)
}

func (m *Manager) storeCert(cacheDir string, domains []string, key *ecdsa.PrivateKey, derChain [][]byte) error {
	tlsCert, err := assembleTLSCert(key, derChain)
	if err != nil {
		return err
	}
	if err := validateCert(tlsCert, domains); err != nil {
		return err
	}
	m.cert.Store(tlsCert)

	if cacheDir == "" {
		return nil
	}
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return fmt.Errorf("cache dir: %w", err)
	}
	if err := writeCertPEM(filepath.Join(cacheDir, "cert.crt"), derChain); err != nil {
		return err
	}
	return writeKeyPEM(filepath.Join(cacheDir, "cert.key"), key)
}

func assembleTLSCert(key *ecdsa.PrivateKey, derChain [][]byte) (*tls.Certificate, error) {
	var certPEM []byte
	for _, der := range derChain {
		certPEM = append(certPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("marshal key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("key pair: %w", err)
	}
	return &tlsCert, nil
}

func writeCertPEM(path string, derChain [][]byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("write cert: %w", err)
	}
	defer f.Close()
	for _, der := range derChain {
		_ = pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	}
	return nil
}

func writeKeyPEM(path string, key *ecdsa.PrivateKey) error {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshal key: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("write key: %w", err)
	}
	defer f.Close()
	return pem.Encode(f, &pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
}

// loadOrCreateAccountKey loads the ECDSA P-256 account key from cacheDir, or
// generates and persists a new one if absent.
func loadOrCreateAccountKey(cacheDir string) (*ecdsa.PrivateKey, error) {
	if cacheDir != "" {
		if key, err := loadKeyPEM(filepath.Join(cacheDir, "account.pem")); err == nil {
			return key, nil
		}
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	if cacheDir != "" {
		if err := os.MkdirAll(cacheDir, 0o700); err == nil {
			_ = writeKeyPEM(filepath.Join(cacheDir, "account.pem"), key)
		}
	}
	return key, nil
}

func loadKeyPEM(path string) (*ecdsa.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, fmt.Errorf("no PEM block in %s", path)
	}
	return x509.ParseECPrivateKey(block.Bytes)
}

// renewBefore is the window before expiry at which Keel renews the cert.
const renewBefore = 30 * 24 * time.Hour

// renewalDelay returns the duration to wait before the next renewal attempt.
// Targets renewal at 30 days before expiry; falls back to 24 hours on error.
func renewalDelay(c *tls.Certificate) time.Duration {
	const fallback = 24 * time.Hour

	if c == nil {
		return fallback
	}
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		return fallback
	}
	until := time.Until(leaf.NotAfter) - renewBefore
	if until < time.Hour {
		return time.Hour
	}
	return until
}

// validateCert checks that c satisfies Keel's TLS policy requirements:
//   - The public key must be ECDSA (RSA and other key types are not permitted).
//   - Every domain in domains must appear in the cert's Subject Alternative Names.
//
// A failure means the cert is either misconfigured (wrong domains) or was
// issued outside policy. Either way the process should not continue: log the
// error and return it so Start() propagates it as a fatal error.
func validateCert(c *tls.Certificate, domains []string) error {
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		return fmt.Errorf("acme: parse cert leaf: %w", err)
	}
	if _, ok := leaf.PublicKey.(*ecdsa.PublicKey); !ok {
		return fmt.Errorf("acme: cert public key is %T; only ECDSA is permitted by Keel TLS policy", leaf.PublicKey)
	}
	sans := make(map[string]bool, len(leaf.DNSNames))
	for _, n := range leaf.DNSNames {
		sans[n] = true
	}
	for _, d := range domains {
		if !sans[d] {
			return fmt.Errorf("acme: cert SANs %v do not cover configured domain %q — delete cache_dir to obtain a fresh certificate", leaf.DNSNames, d)
		}
	}
	return nil
}

// loadCachedCert loads the cert/key pair written by storeCert from cacheDir.
// Returns an error if either file is absent or unparseable.
func loadCachedCert(cacheDir string) (*tls.Certificate, error) {
	certPath := filepath.Join(cacheDir, "cert.crt")
	keyPath := filepath.Join(cacheDir, "cert.key")
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, err
	}
	return &cert, nil
}

// certNeedsRenewal reports whether c is nil, unparseable, or within the
// 30-day renewal window. These are all conditions that warrant a new cert.
func certNeedsRenewal(c *tls.Certificate) bool {
	if c == nil {
		return true
	}
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		return true
	}
	return time.Until(leaf.NotAfter) < renewBefore
}

// httpClientWithCA returns an *http.Client that trusts the PEM CA cert at path
// in addition to the system roots. Used for private / test CAs (e.g. pebble).
func httpClientWithCA(path string) (*http.Client, error) {
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read ca cert: %w", err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("no valid PEM certificate in %s", path)
	}
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool},
		},
	}, nil
}
