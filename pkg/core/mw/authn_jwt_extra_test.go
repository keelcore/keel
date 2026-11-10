//go:build !no_authn

package mw

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
)

// ---------------------------------------------------------------------------
// AuthnJWT handler
// ---------------------------------------------------------------------------

func TestAuthnJWT_MissingBearer_401(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	h := AuthnJWT(config.Config{}, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), log)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for missing bearer, got %d", rr.Code)
	}
}

func TestAuthnJWT_InvalidToken_401(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{Authn: config.AuthnConfig{TrustedSigners: []string{"secret-key"}}}
	h := AuthnJWT(cfg, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), log)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("authorization", "Bearer not.a.valid.token")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for invalid token, got %d", rr.Code)
	}
}

func TestAuthnJWT_ValidToken_NoAllowList_200(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{Authn: config.AuthnConfig{TrustedSigners: []string{"secret-key"}}}
	called := false
	h := AuthnJWT(cfg, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}), log)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("authorization", "Bearer "+hs256Token(t, "secret-key", "alice"))
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !called {
		t.Errorf("expected 200 and inner called, got %d called=%v", rr.Code, called)
	}
}

func TestAuthnJWT_ValidToken_AllowedSub_200(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{"secret-key"},
		TrustedIDs:     []string{"alice"},
	}}
	h := AuthnJWT(cfg, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), log)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("authorization", "Bearer "+hs256Token(t, "secret-key", "alice"))
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for allowed sub, got %d", rr.Code)
	}
}

func TestAuthnJWT_ValidToken_ForbiddenSub_403(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{"secret-key"},
		TrustedIDs:     []string{"bob"},
	}}
	h := AuthnJWT(cfg, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), log)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("authorization", "Bearer "+hs256Token(t, "secret-key", "alice"))
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403 for forbidden sub, got %d", rr.Code)
	}
}

func TestAuthnJWT_GetSignersDynamic_200(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	getSigners := func() []string { return []string{"secret-key"} }
	h := AuthnJWT(config.Config{}, getSigners, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), log)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("authorization", "Bearer "+hs256Token(t, "secret-key", "alice"))
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 via dynamic getSigners, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// parseWithAllSigners — success after an earlier signer fails
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// signerKeyfunc — http signer routes to the JWKS cache
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// resolveStaticKey — PEM branch
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// parsePEMPublicKey — all branches
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// jwksCache.keysFor
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// fetchJWKS — decode error
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// parseRSAJWK / parseECJWK — error branches (routed through parseJWK)
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// NewJWTSigner — read-error and success paths
// ---------------------------------------------------------------------------
