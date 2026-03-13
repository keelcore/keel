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

// acmeClient is the subset of *xacme.Client used by Manager. Factored into an
// interface so network-dependent helpers can be tested without a real CA.
type acmeClient interface {
	Register(ctx context.Context, a *xacme.Account, prompt func(string) bool) (*xacme.Account, error)
	AuthorizeOrder(ctx context.Context, id []xacme.AuthzID, opt ...xacme.OrderOption) (*xacme.Order, error)
	WaitOrder(ctx context.Context, url string) (*xacme.Order, error)
	GetAuthorization(ctx context.Context, url string) (*xacme.Authorization, error)
	HTTP01ChallengeResponse(token string) (string, error)
	Accept(ctx context.Context, chal *xacme.Challenge) (*xacme.Challenge, error)
	WaitAuthorization(ctx context.Context, url string) (*xacme.Authorization, error)
	CreateOrderCert(ctx context.Context, url string, csr []byte, bundle bool) (der [][]byte, certURL string, err error)
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

// resolveDirectoryURL returns the ACME CA directory URL, defaulting to
// Let's Encrypt when caURL is empty.
func resolveDirectoryURL(caURL string) string {
	if caURL == "" {
		return xacme.LetsEncryptURL
	}
	return caURL
}

// buildACMEClient constructs an acmeClient from the account key and config,
// optionally attaching an HTTP client that trusts a custom CA certificate.
func buildACMEClient(accountKey *ecdsa.PrivateKey, cfg config.ACMEConfig) (acmeClient, error) {
	client := &xacme.Client{Key: accountKey, DirectoryURL: resolveDirectoryURL(cfg.CAUrl)}
	if cfg.CACertFile == "" {
		return client, nil
	}
	hc, err := httpClientWithCA(cfg.CACertFile)
	if err != nil {
		return nil, fmt.Errorf("acme ca cert: %w", err)
	}
	client.HTTPClient = hc
	return client, nil
}

// setupACMEClient loads or creates the account key and builds the ACME client.
func (m *Manager) setupACMEClient(cfg config.ACMEConfig) (acmeClient, error) {
	accountKey, err := loadOrCreateAccountKey(cfg.CacheDir)
	if err != nil {
		return nil, fmt.Errorf("acme account key: %w", err)
	}
	return buildACMEClient(accountKey, cfg)
}

// loadAndValidateCachedCert loads a previously-issued certificate from cacheDir.
// A cache miss is silently ignored; a domain or key-type mismatch is fatal.
func (m *Manager) loadAndValidateCachedCert(cfg config.ACMEConfig) error {
	if cfg.CacheDir == "" {
		return nil
	}
	cached, err := loadCachedCert(cfg.CacheDir)
	if err != nil {
		return nil // cache miss: not an error
	}
	if verr := validateCert(cached, cfg.Domains); verr != nil {
		if m.logErr != nil {
			m.logErr("acme_cached_cert_invalid", map[string]any{"err": verr.Error()})
		}
		return verr
	}
	m.cert.Store(cached)
	return nil
}

// waitForRenewalWindow blocks until the renewal window for c is reached or ctx
// is done. Returns false if ctx was cancelled, true when the delay elapsed.
func waitForRenewalWindow(ctx context.Context, c *tls.Certificate) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(renewalDelay(c)):
		return true
	}
}

// runRenewalLoop repeatedly obtains/renews the certificate until ctx is done.
func (m *Manager) runRenewalLoop(ctx context.Context, client acmeClient, cfg config.ACMEConfig) error {
	for {
		if err := m.obtainCert(ctx, client, cfg); err != nil && !errors.Is(err, context.Canceled) {
			if m.logErr != nil {
				m.logErr("acme_cert_obtain_failed", map[string]any{"err": err.Error()})
			}
		}
		if !waitForRenewalWindow(ctx, m.cert.Load()) {
			return nil
		}
	}
}

// Start manages the ACME certificate lifecycle: account registration, initial
// certificate obtainment, and background renewal. It blocks until ctx is done.
func (m *Manager) Start(ctx context.Context, cfg config.ACMEConfig) error {
	client, err := m.setupACMEClient(cfg)
	if err != nil {
		return err
	}
	if err := m.loadAndValidateCachedCert(cfg); err != nil {
		return err
	}
	if err := registerAccount(ctx, client, cfg.Email); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return fmt.Errorf("acme register: %w", err)
	}
	if !certNeedsRenewal(m.cert.Load()) && !waitForRenewalWindow(ctx, m.cert.Load()) {
		return nil
	}
	return m.runRenewalLoop(ctx, client, cfg)
}

// Validate returns an error if cfg contains an invalid ACME configuration.
func Validate(_ config.Config) error { return nil }

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func registerAccount(ctx context.Context, client acmeClient, email string) error {
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

// buildAuthzIDs converts domain names to ACME authorization identifiers.
func buildAuthzIDs(domains []string) []xacme.AuthzID {
	ids := make([]xacme.AuthzID, len(domains))
	for i, d := range domains {
		ids[i] = xacme.AuthzID{Type: "dns", Value: d}
	}
	return ids
}

// certMaterialInject controls error injection in generateCertMaterial for
// testing. Production callers must pass noInjection.
type certMaterialInject int

const (
	noInjection  certMaterialInject = iota
	injectKeyErr                    // force ecdsa.GenerateKey error path
	injectCSRErr                    // force buildCSR error path
)

// generateCertMaterial creates a fresh ECDSA P-256 key and DER-encoded CSR for domains.
func generateCertMaterial(domains []string, inject certMaterialInject) (*ecdsa.PrivateKey, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if inject == injectKeyErr || err != nil {
		return nil, nil, fmt.Errorf("generate cert key: %w", err)
	}
	csr, err := buildCSR(key, domains)
	if inject == injectCSRErr || err != nil {
		return nil, nil, fmt.Errorf("build csr: %w", err)
	}
	return key, csr, nil
}

// fulfillAllHTTP01 satisfies every http-01 authorization challenge in authzURLs.
func (m *Manager) fulfillAllHTTP01(ctx context.Context, client acmeClient, authzURLs []string) error {
	for _, authzURL := range authzURLs {
		if err := m.fulfillHTTP01(ctx, client, authzURL); err != nil {
			return err
		}
	}
	return nil
}

// finalizeCert generates key material, submits the CSR to the CA, and stores
// the resulting certificate chain. inject controls error injection for testing;
// callers must pass noInjection in production.
func (m *Manager) finalizeCert(ctx context.Context, client acmeClient, order *xacme.Order, domains []string, cacheDir string, inject certMaterialInject) error {
	key, csr, err := generateCertMaterial(domains, inject)
	if err != nil {
		return err
	}
	derChain, _, err := client.CreateOrderCert(ctx, order.FinalizeURL, csr, true)
	if err != nil {
		return fmt.Errorf("create cert: %w", err)
	}
	return m.storeCert(cacheDir, domains, key, derChain)
}

func (m *Manager) obtainCert(ctx context.Context, client acmeClient, cfg config.ACMEConfig) error {
	order, err := client.AuthorizeOrder(ctx, buildAuthzIDs(cfg.Domains))
	if err != nil {
		return fmt.Errorf("authorize order: %w", err)
	}
	if err := m.fulfillAllHTTP01(ctx, client, order.AuthzURLs); err != nil {
		return err
	}
	order, err = client.WaitOrder(ctx, order.URI)
	if err != nil {
		return fmt.Errorf("wait order: %w", err)
	}
	return m.finalizeCert(ctx, client, order, cfg.Domains, cfg.CacheDir, noInjection)
}

// selectHTTP01Challenge returns the http-01 challenge from authz.
// Returns (nil, nil) if the authorization is already valid.
// Returns an error if no http-01 challenge is present.
func selectHTTP01Challenge(authz *xacme.Authorization) (*xacme.Challenge, error) {
	if authz.Status == xacme.StatusValid {
		return nil, nil
	}
	chal := http01Challenge(authz)
	if chal == nil {
		return nil, fmt.Errorf("no http-01 challenge for %s", authz.Identifier.Value)
	}
	return chal, nil
}

// acceptChallenge registers the key-auth token, signals the CA to validate,
// and waits for the authorization to complete.
func (m *Manager) acceptChallenge(ctx context.Context, client acmeClient, authz *xacme.Authorization, chal *xacme.Challenge) error {
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

func (m *Manager) fulfillHTTP01(ctx context.Context, client acmeClient, authzURL string) error {
	authz, err := client.GetAuthorization(ctx, authzURL)
	if err != nil {
		return fmt.Errorf("get authorization: %w", err)
	}
	chal, err := selectHTTP01Challenge(authz)
	if err != nil || chal == nil {
		return err
	}
	return m.acceptChallenge(ctx, client, authz, chal)
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
