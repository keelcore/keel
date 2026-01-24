//go:build !no_authn

package unit

import (
    "net/http"
    "net/http/httptest"
    "testing"
    "time"

    "github.com/golang-jwt/jwt/v5"

    "github.com/keelcore/keel/pkg/config"
    "github.com/keelcore/keel/pkg/core/logging"
    "github.com/keelcore/keel/pkg/core/mw"
)

func TestAuthnJWT_AllowsTrustedSub(t *testing.T) {
    // >= 112 bits required under GODEBUG=fips140=only.
    const hmac_key = "0123456789abcdef" // 16 bytes

    cfg := config.Config{
        TrustedIDs:     []string{"alice"},
        TrustedSigners: []string{hmac_key},
        AuthnEnabled:   true,
    }

    token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
        "sub": "alice",
        "exp": time.Now().Add(5 * time.Minute).Unix(),
    })
    raw, err := token.SignedString([]byte(hmac_key))
    if err != nil {
        t.Fatal(err)
    }

    h := mw.AuthnJWT(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
        w.WriteHeader(200)
    }), logging.New(logging.Config{JSON: false}))

    req := httptest.NewRequest("GET", "http://example.com/", nil)
    req.Header.Set("authorization", "Bearer "+raw)
    rr := httptest.NewRecorder()
    h.ServeHTTP(rr, req)

    if rr.Code != 200 {
        t.Fatalf("expected 200, got %d", rr.Code)
    }
}
