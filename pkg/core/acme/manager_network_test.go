//go:build !no_acme

package acme

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"

	xacme "golang.org/x/crypto/acme"
)

// Tests for network-dependent ACME helpers using a stub acmeClient.
// These cover fulfillHTTP01, acceptChallenge, obtainCert, finalizeCert,
// fulfillAllHTTP01, and runRenewalLoop without contacting a real CA.

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

// ---------------------------------------------------------------------------
// acceptChallenge tests
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// obtainCert tests
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// finalizeCert tests
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// fulfillAllHTTP01 tests
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// runRenewalLoop tests
// ---------------------------------------------------------------------------
