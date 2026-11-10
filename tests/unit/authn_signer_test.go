//go:build !no_authn

package unit

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/mw"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// NewJWTSigner
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// loadTrustedSigners (via AuthnJWT integration)
// ---------------------------------------------------------------------------

func writeTrustedSignersFile(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp("", "signers-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(f.Name()) })
	_, _ = f.WriteString(content)
	f.Close()
	return f.Name()
}

func TestAuthn_TrustedSignersFile_MergesWithInline(t *testing.T) {
	const key1 = "0123456789abcdef"
	const key2 = "fedcba9876543210"

	sigFile := writeTrustedSignersFile(t, key2+"\n")

	cfg := config.Config{
		Authn: config.AuthnConfig{
			Enabled:            true,
			TrustedSigners:     []string{key1},
			TrustedSignersFile: sigFile,
		},
	}
	log := logging.New(logging.Config{Out: &strings.Builder{}})

	// Token signed with key2 (from file) should be accepted.
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "any",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	raw, err := token.SignedString([]byte(key2))
	if err != nil {
		t.Fatal(err)
	}

	var reached bool
	h := mw.AuthnJWT(cfg, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
	}), log)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if !reached {
		t.Errorf("handler not reached; status=%d", rr.Code)
	}
}

func TestAuthn_TrustedSignersFile_MissingFile_WarnsAndContinues(t *testing.T) {
	const key = "0123456789abcdef"

	var logBuf strings.Builder
	log := logging.New(logging.Config{JSON: true, Out: &logBuf})

	cfg := config.Config{
		Authn: config.AuthnConfig{
			Enabled:            true,
			TrustedSigners:     []string{key},
			TrustedSignersFile: "/nonexistent/signers.txt",
		},
	}

	// Inline signer still works despite missing file.
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "any",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	raw, _ := token.SignedString([]byte(key))

	var reached bool
	h := mw.AuthnJWT(cfg, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
	}), log)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if !reached {
		t.Error("inline signer should still work when file is missing")
	}
	if !strings.Contains(logBuf.String(), "trusted_signers_file_open_failed") {
		t.Error("expected warning about missing signers file")
	}
}

func TestAuthn_TrustedSignersFile_CommentsAndBlankLines(t *testing.T) {
	const key = "0123456789abcdef"

	content := "# this is a comment\n\n" + key + "\n\n# another comment\n"
	sigFile := writeTrustedSignersFile(t, content)

	cfg := config.Config{
		Authn: config.AuthnConfig{
			Enabled:            true,
			TrustedSignersFile: sigFile,
		},
	}
	log := logging.New(logging.Config{Out: &strings.Builder{}})

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "any",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	raw, _ := token.SignedString([]byte(key))

	var reached bool
	h := mw.AuthnJWT(cfg, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
	}), log)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if !reached {
		t.Error("key from file should be accepted when comments and blank lines are present")
	}
}
