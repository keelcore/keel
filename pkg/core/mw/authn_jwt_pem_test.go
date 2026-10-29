//go:build !no_authn

package mw

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
)

// writeSignersFile writes content to a temp signers file and returns its path.
func writeSignersFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "signers.txt")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func loadFromFile(t *testing.T, content string) []string {
	t.Helper()
	cfg := config.AuthnConfig{TrustedSignersFile: writeSignersFile(t, content)}
	return LoadTrustedSigners(cfg, logging.New(logging.Config{Out: io.Discard}))
}

// A multi-line PEM public key in the signers file becomes exactly ONE signer
// (the whole block), alongside one-per-line HMAC secrets.
func TestLoadTrustedSigners_PEMBlockIsOneSigner(t *testing.T) {
	pemKey := rsaPubPEM(t)
	got := loadFromFile(t, "hmac-secret-1\n"+pemKey+"\n\nhmac-secret-2\n")
	if len(got) != 3 {
		t.Fatalf("expected 3 signers (2 HMAC + 1 PEM), got %d: %q", len(got), got)
	}
	if got[0] != "hmac-secret-1" || got[2] != "hmac-secret-2" {
		t.Errorf("HMAC secrets not preserved in order: %q", got)
	}
	if _, err := parsePEMPublicKey(got[1], "RS256"); err != nil {
		t.Errorf("PEM signer did not parse as a public key: %v", err)
	}
}

// Blank lines inside a PEM block are tolerated and do not split the block.
func TestLoadTrustedSigners_PEMBlockWithBlankLine(t *testing.T) {
	pemKey := rsaPubPEM(t)
	lines := strings.Split(strings.TrimSpace(pemKey), "\n")
	withBlank := lines[0] + "\n\n" + strings.Join(lines[1:], "\n") + "\n"
	got := loadFromFile(t, withBlank)
	if len(got) != 1 {
		t.Fatalf("expected 1 PEM signer, got %d: %q", len(got), got)
	}
	if _, err := parsePEMPublicKey(got[0], "RS256"); err != nil {
		t.Errorf("PEM signer did not parse: %v", err)
	}
}

// PEM armor outside a block is dropped, never accepted as an HMAC secret.
func TestLoadTrustedSigners_StrayArmorDropped(t *testing.T) {
	got := loadFromFile(t, "-----END PUBLIC KEY-----\nhmac-secret\n")
	if len(got) != 1 || got[0] != "hmac-secret" {
		t.Errorf("expected only the HMAC secret, got %q", got)
	}
}

// An unterminated PEM block is dropped (and its lines never become secrets).
func TestLoadTrustedSigners_UnterminatedPEMDropped(t *testing.T) {
	got := loadFromFile(t, "hmac-secret\n-----BEGIN PUBLIC KEY-----\nMIIBIjANBgkqhkiG9w0BAQEF\n")
	if len(got) != 1 || got[0] != "hmac-secret" {
		t.Errorf("expected only the HMAC secret, got %q", got)
	}
}

// Regression for the forgery: with a PEM key in the signers file, a token
// HMAC-signed using any line of that PEM (here the END armor, which an attacker
// can guess) must be rejected.
func TestAuthnJWT_PEMArmorIsNotAnHMACSecret(t *testing.T) {
	cfg := config.Config{}
	cfg.Authn.TrustedSignersFile = writeSignersFile(t, rsaPubPEM(t))
	log := logging.New(logging.Config{Out: io.Discard})
	signers := LoadTrustedSigners(cfg.Authn, log)
	for _, armor := range []string{"-----END PUBLIC KEY-----", "-----BEGIN PUBLIC KEY-----"} {
		forged := hs256Token(t, armor, "attacker")
		if _, err := parseWithAllSigners(forged, signers, newJWKSCache()); err == nil {
			t.Errorf("token HMAC-signed with %q was accepted", armor)
		}
	}
}

// An inline signer that is a PEM fragment is never usable as an HMAC secret.
func TestResolveStaticKey_ArmorFragmentNotHMAC(t *testing.T) {
	if _, err := resolveStaticKey("-----END PUBLIC KEY-----", "HS256"); err == nil {
		t.Error("expected error: PEM armor must not resolve to an HMAC key")
	}
}
