//go:build !no_acme

package acme

// Tests for network-dependent ACME helpers using a stub acmeClient.
// These cover fulfillHTTP01, acceptChallenge, obtainCert, finalizeCert,
// fulfillAllHTTP01, and runRenewalLoop without contacting a real CA.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"testing"
	"time"

	xacme "golang.org/x/crypto/acme"

	"github.com/keelcore/keel/pkg/config"
)

// ---------------------------------------------------------------------------
// stubACMEClient
// ---------------------------------------------------------------------------

type stubACMEClient struct {
	registerFn                func(context.Context, *xacme.Account, func(string) bool) (*xacme.Account, error)
	authorizeOrderFn          func(context.Context, []xacme.AuthzID, ...xacme.OrderOption) (*xacme.Order, error)
	waitOrderFn               func(context.Context, string) (*xacme.Order, error)
	getAuthorizationFn        func(context.Context, string) (*xacme.Authorization, error)
	http01ChallengeResponseFn func(string) (string, error)
	acceptFn                  func(context.Context, *xacme.Challenge) (*xacme.Challenge, error)
	waitAuthorizationFn       func(context.Context, string) (*xacme.Authorization, error)
	createOrderCertFn         func(context.Context, string, []byte, bool) ([][]byte, string, error)
}

func (s *stubACMEClient) Register(ctx context.Context, a *xacme.Account, prompt func(string) bool) (*xacme.Account, error) {
	if s.registerFn != nil {
		return s.registerFn(ctx, a, prompt)
	}
	return a, nil
}

func (s *stubACMEClient) AuthorizeOrder(ctx context.Context, id []xacme.AuthzID, opt ...xacme.OrderOption) (*xacme.Order, error) {
	if s.authorizeOrderFn != nil {
		return s.authorizeOrderFn(ctx, id, opt...)
	}
	return &xacme.Order{}, nil
}

func (s *stubACMEClient) WaitOrder(ctx context.Context, url string) (*xacme.Order, error) {
	if s.waitOrderFn != nil {
		return s.waitOrderFn(ctx, url)
	}
	return &xacme.Order{}, nil
}

func (s *stubACMEClient) GetAuthorization(ctx context.Context, url string) (*xacme.Authorization, error) {
	if s.getAuthorizationFn != nil {
		return s.getAuthorizationFn(ctx, url)
	}
	return &xacme.Authorization{Status: xacme.StatusValid}, nil
}

func (s *stubACMEClient) HTTP01ChallengeResponse(token string) (string, error) {
	if s.http01ChallengeResponseFn != nil {
		return s.http01ChallengeResponseFn(token)
	}
	return "key-auth", nil
}

func (s *stubACMEClient) Accept(ctx context.Context, chal *xacme.Challenge) (*xacme.Challenge, error) {
	if s.acceptFn != nil {
		return s.acceptFn(ctx, chal)
	}
	return chal, nil
}

func (s *stubACMEClient) WaitAuthorization(ctx context.Context, url string) (*xacme.Authorization, error) {
	if s.waitAuthorizationFn != nil {
		return s.waitAuthorizationFn(ctx, url)
	}
	return &xacme.Authorization{Status: xacme.StatusValid}, nil
}

func (s *stubACMEClient) CreateOrderCert(ctx context.Context, url string, csr []byte, bundle bool) ([][]byte, string, error) {
	if s.createOrderCertFn != nil {
		return s.createOrderCertFn(ctx, url, csr, bundle)
	}
	return nil, "", nil
}

// stubSignedCert returns a CreateOrderCert stub that parses the CSR's public
// key and issues a CA-signed leaf certificate containing it. This lets
// tls.X509KeyPair succeed inside storeCert without a real ACME CA.
func stubSignedCert(t *testing.T) func(context.Context, string, []byte, bool) ([][]byte, string, error) {
	t.Helper()
	return func(_ context.Context, _ string, csr []byte, _ bool) ([][]byte, string, error) {
		csrParsed, err := x509.ParseCertificateRequest(csr)
		if err != nil {
			return nil, "", err
		}
		caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, "", err
		}
		caTmpl := &x509.Certificate{
			SerialNumber:          big.NewInt(1),
			Subject:               pkix.Name{CommonName: "stub-ca"},
			NotBefore:             time.Now().Add(-time.Hour),
			NotAfter:              time.Now().Add(90 * 24 * time.Hour),
			IsCA:                  true,
			BasicConstraintsValid: true,
		}
		caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
		if err != nil {
			return nil, "", err
		}
		caCert, err := x509.ParseCertificate(caDER)
		if err != nil {
			return nil, "", err
		}
		leafTmpl := &x509.Certificate{
			SerialNumber: big.NewInt(2),
			Subject:      pkix.Name{CommonName: csrParsed.DNSNames[0]},
			DNSNames:     csrParsed.DNSNames,
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(90 * 24 * time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		}
		leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, csrParsed.PublicKey, caKey)
		if err != nil {
			return nil, "", err
		}
		return [][]byte{leafDER}, "", nil
	}
}

// ---------------------------------------------------------------------------
// fulfillHTTP01 tests
// ---------------------------------------------------------------------------

func TestFulfillHTTP01_GetAuthorizationError(t *testing.T) {
	m := New()
	stub := &stubACMEClient{
		getAuthorizationFn: func(_ context.Context, _ string) (*xacme.Authorization, error) {
			return nil, errors.New("get-auth-fail")
		},
	}
	err := m.fulfillHTTP01(context.Background(), stub, "http://ca/authz/1")
	if err == nil {
		t.Fatal("expected error from GetAuthorization failure")
	}
}

func TestFulfillHTTP01_AlreadyValid(t *testing.T) {
	m := New()
	stub := &stubACMEClient{
		getAuthorizationFn: func(_ context.Context, _ string) (*xacme.Authorization, error) {
			return &xacme.Authorization{Status: xacme.StatusValid}, nil
		},
	}
	if err := m.fulfillHTTP01(context.Background(), stub, "http://ca/authz/1"); err != nil {
		t.Fatalf("expected nil for already-valid authz, got %v", err)
	}
}

func TestFulfillHTTP01_SuccessAcceptsChallenge(t *testing.T) {
	m := New()
	stub := &stubACMEClient{
		getAuthorizationFn: func(_ context.Context, _ string) (*xacme.Authorization, error) {
			return &xacme.Authorization{
				Status: xacme.StatusPending,
				URI:    "http://ca/authz/1",
				Challenges: []*xacme.Challenge{
					{Type: "http-01", Token: "tok1"},
				},
			}, nil
		},
	}
	if err := m.fulfillHTTP01(context.Background(), stub, "http://ca/authz/1"); err != nil {
		t.Fatalf("expected nil for successful challenge, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// acceptChallenge tests
// ---------------------------------------------------------------------------

func TestAcceptChallenge_HTTP01ChallengeResponseError(t *testing.T) {
	m := New()
	stub := &stubACMEClient{
		http01ChallengeResponseFn: func(_ string) (string, error) {
			return "", errors.New("key-auth-fail")
		},
	}
	authz := &xacme.Authorization{URI: "http://ca/authz/1"}
	chal := &xacme.Challenge{Type: "http-01", Token: "tok"}
	err := m.acceptChallenge(context.Background(), stub, authz, chal)
	if err == nil {
		t.Fatal("expected error from HTTP01ChallengeResponse failure")
	}
}

func TestAcceptChallenge_AcceptError(t *testing.T) {
	m := New()
	stub := &stubACMEClient{
		acceptFn: func(_ context.Context, _ *xacme.Challenge) (*xacme.Challenge, error) {
			return nil, errors.New("accept-fail")
		},
	}
	authz := &xacme.Authorization{URI: "http://ca/authz/1"}
	chal := &xacme.Challenge{Type: "http-01", Token: "tok"}
	err := m.acceptChallenge(context.Background(), stub, authz, chal)
	if err == nil {
		t.Fatal("expected error from Accept failure")
	}
}

func TestAcceptChallenge_WaitAuthorizationError(t *testing.T) {
	m := New()
	stub := &stubACMEClient{
		waitAuthorizationFn: func(_ context.Context, _ string) (*xacme.Authorization, error) {
			return nil, errors.New("wait-auth-fail")
		},
	}
	authz := &xacme.Authorization{URI: "http://ca/authz/1"}
	chal := &xacme.Challenge{Type: "http-01", Token: "tok"}
	err := m.acceptChallenge(context.Background(), stub, authz, chal)
	if err == nil {
		t.Fatal("expected error from WaitAuthorization failure")
	}
}

func TestAcceptChallenge_Success(t *testing.T) {
	m := New()
	stub := &stubACMEClient{}
	authz := &xacme.Authorization{URI: "http://ca/authz/1"}
	chal := &xacme.Challenge{Type: "http-01", Token: "tok"}
	if err := m.acceptChallenge(context.Background(), stub, authz, chal); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// obtainCert tests
// ---------------------------------------------------------------------------

func TestObtainCert_AuthorizeOrderError(t *testing.T) {
	m := New()
	stub := &stubACMEClient{
		authorizeOrderFn: func(_ context.Context, _ []xacme.AuthzID, _ ...xacme.OrderOption) (*xacme.Order, error) {
			return nil, errors.New("authorize-fail")
		},
	}
	cfg := config.ACMEConfig{Domains: []string{"example.com"}}
	if err := m.obtainCert(context.Background(), stub, cfg); err == nil {
		t.Fatal("expected error from AuthorizeOrder failure")
	}
}

func TestObtainCert_FulfillAllHTTP01Error(t *testing.T) {
	m := New()
	stub := &stubACMEClient{
		authorizeOrderFn: func(_ context.Context, _ []xacme.AuthzID, _ ...xacme.OrderOption) (*xacme.Order, error) {
			// Return an order with an AuthzURL so fulfillAllHTTP01 is exercised.
			return &xacme.Order{URI: "http://ca/order/1", AuthzURLs: []string{"http://ca/authz/1"}}, nil
		},
		getAuthorizationFn: func(_ context.Context, _ string) (*xacme.Authorization, error) {
			return nil, errors.New("get-auth-fail")
		},
	}
	cfg := config.ACMEConfig{Domains: []string{"example.com"}}
	if err := m.obtainCert(context.Background(), stub, cfg); err == nil {
		t.Fatal("expected error from fulfillAllHTTP01 failure")
	}
}

func TestObtainCert_WaitOrderError(t *testing.T) {
	m := New()
	stub := &stubACMEClient{
		authorizeOrderFn: func(_ context.Context, _ []xacme.AuthzID, _ ...xacme.OrderOption) (*xacme.Order, error) {
			return &xacme.Order{URI: "http://ca/order/1"}, nil
		},
		waitOrderFn: func(_ context.Context, _ string) (*xacme.Order, error) {
			return nil, errors.New("wait-order-fail")
		},
	}
	cfg := config.ACMEConfig{Domains: []string{"example.com"}}
	if err := m.obtainCert(context.Background(), stub, cfg); err == nil {
		t.Fatal("expected error from WaitOrder failure")
	}
}

func TestObtainCert_Success(t *testing.T) {
	domains := []string{"example.com"}
	m := New()
	stub := &stubACMEClient{
		authorizeOrderFn: func(_ context.Context, _ []xacme.AuthzID, _ ...xacme.OrderOption) (*xacme.Order, error) {
			return &xacme.Order{URI: "http://ca/order/1", FinalizeURL: "http://ca/order/1/finalize"}, nil
		},
		waitOrderFn: func(_ context.Context, _ string) (*xacme.Order, error) {
			return &xacme.Order{FinalizeURL: "http://ca/order/1/finalize"}, nil
		},
		createOrderCertFn: stubSignedCert(t),
	}
	cfg := config.ACMEConfig{Domains: domains}
	if err := m.obtainCert(context.Background(), stub, cfg); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if m.cert.Load() == nil {
		t.Error("expected cert to be stored after obtainCert success")
	}
}

// ---------------------------------------------------------------------------
// finalizeCert tests
// ---------------------------------------------------------------------------

func TestFinalizeCert_InjectKeyError(t *testing.T) {
	m := New()
	order := &xacme.Order{FinalizeURL: "http://ca/order/1/finalize"}
	if err := m.finalizeCert(context.Background(), &stubACMEClient{}, order, []string{"example.com"}, "", injectKeyErr); err == nil {
		t.Fatal("expected error from injected key generation failure")
	}
}

func TestFinalizeCert_InjectCSRError(t *testing.T) {
	m := New()
	order := &xacme.Order{FinalizeURL: "http://ca/order/1/finalize"}
	if err := m.finalizeCert(context.Background(), &stubACMEClient{}, order, []string{"example.com"}, "", injectCSRErr); err == nil {
		t.Fatal("expected error from injected CSR build failure")
	}
}

func TestFinalizeCert_CreateOrderCertError(t *testing.T) {
	m := New()
	stub := &stubACMEClient{
		createOrderCertFn: func(_ context.Context, _ string, _ []byte, _ bool) ([][]byte, string, error) {
			return nil, "", errors.New("create-cert-fail")
		},
	}
	order := &xacme.Order{FinalizeURL: "http://ca/order/1/finalize"}
	if err := m.finalizeCert(context.Background(), stub, order, []string{"example.com"}, "", noInjection); err == nil {
		t.Fatal("expected error from CreateOrderCert failure")
	}
}

func TestFinalizeCert_Success(t *testing.T) {
	domains := []string{"example.com"}
	m := New()
	stub := &stubACMEClient{createOrderCertFn: stubSignedCert(t)}
	order := &xacme.Order{FinalizeURL: "http://ca/order/1/finalize"}
	if err := m.finalizeCert(context.Background(), stub, order, domains, "", noInjection); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if m.cert.Load() == nil {
		t.Error("expected cert to be stored after finalizeCert success")
	}
}

// ---------------------------------------------------------------------------
// fulfillAllHTTP01 tests
// ---------------------------------------------------------------------------

func TestFulfillAllHTTP01_FirstURLFails(t *testing.T) {
	m := New()
	stub := &stubACMEClient{
		getAuthorizationFn: func(_ context.Context, _ string) (*xacme.Authorization, error) {
			return nil, errors.New("get-auth-fail")
		},
	}
	err := m.fulfillAllHTTP01(context.Background(), stub, []string{"http://ca/authz/1", "http://ca/authz/2"})
	if err == nil {
		t.Fatal("expected error when first URL fails")
	}
}

func TestFulfillAllHTTP01_AllSucceed(t *testing.T) {
	m := New()
	stub := &stubACMEClient{} // default: GetAuthorization returns StatusValid
	urls := []string{"http://ca/authz/1", "http://ca/authz/2"}
	if err := m.fulfillAllHTTP01(context.Background(), stub, urls); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// runRenewalLoop tests
// ---------------------------------------------------------------------------

// TestRunRenewalLoop_ContextCancel verifies the loop exits when ctx is done.
// AuthorizeOrder returns context.Canceled (swallowed), then waitForRenewalWindow
// sees the done ctx and returns false.
func TestRunRenewalLoop_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m := New()
	stub := &stubACMEClient{
		authorizeOrderFn: func(_ context.Context, _ []xacme.AuthzID, _ ...xacme.OrderOption) (*xacme.Order, error) {
			return nil, context.Canceled
		},
	}
	cfg := config.ACMEConfig{Domains: []string{"example.com"}}
	if err := m.runRenewalLoop(ctx, stub, cfg); err != nil {
		t.Fatalf("expected nil from cancelled context, got %v", err)
	}
}

// TestRunRenewalLoop_ObtainErrorLogged verifies that a non-context.Canceled
// error from obtainCert is passed to the logger and the loop still exits
// cleanly when the context is cancelled.
func TestRunRenewalLoop_ObtainErrorLogged(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m := New()
	var logged string
	m.SetLogger(func(msg string, _ map[string]any) { logged = msg })
	stub := &stubACMEClient{
		authorizeOrderFn: func(_ context.Context, _ []xacme.AuthzID, _ ...xacme.OrderOption) (*xacme.Order, error) {
			return nil, errors.New("transient-error")
		},
	}
	cfg := config.ACMEConfig{Domains: []string{"example.com"}}
	if err := m.runRenewalLoop(ctx, stub, cfg); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if logged != "acme_cert_obtain_failed" {
		t.Errorf("expected acme_cert_obtain_failed logged, got %q", logged)
	}
}
