//go:build !no_authn

package mw

// Fuzz targets for untrusted-input parsing in authn: JWKS documents fetched
// from remote endpoints, bearer tokens from requests, and PEM key material.
// Run one target with: go test -run '^$' -fuzz '^FuzzParseJWK$' ./pkg/core/mw/

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"testing"
)

func FuzzParseJWK(f *testing.F) {
	f.Add([]byte(`{"kty":"RSA","kid":"k1","n":"sXchDaQebHnPiGvyDOAT4saGEUetSyo9MKLOoWFsueri23bOdgWp4Dy1WlUzewbgBHod5pcM9H95GQRV3JDXboIRROSBigeC5yjU1hGzHHyXss8UDprecbAYxknTcQkhslANGRUZmdTOQ5qTRsLAt6BTYuyvVRdhS8exSZEy_c4gs_7svlJJQ4H9_NxsiIoLwAEk7-Q3UXERGYw_75IDrGA84-lA_-Ct4eTlXHBIY2EaV7t7LjJaynVJCpkv4LKjTTAumiGUIuQhrNhZLuF_RJLqHpM2kgWFLU7-VTdL1VbC2tejvcI2BlMkEpk1BzBZI0KQB0GaDWFLN-aEAw3vRw","e":"AQAB"}`))
	f.Add([]byte(`{"kty":"EC","crv":"P-256","x":"MKBCTNIcKUSDii11ySs3526iDZ8AiTo7Tu6KPAqv7D4","y":"4Etl6SRW2YiLUrN5vfvVHuhp7x8PxltmWWlbbM4IFyM"}`))
	f.Add([]byte(`{"kty":"oct"}`))
	f.Add([]byte(`{`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		k, err := parseJWK(json.RawMessage(raw))
		if err == nil && k.key == nil {
			t.Fatalf("parseJWK returned no error and no key for %q", raw)
		}
	})
}

func FuzzParseWithAllSigners(f *testing.F) {
	ecPEM := fuzzECPublicPEM(f)
	f.Add("eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ4In0.signature", "0123456789abcdef")
	f.Add("eyJhbGciOiJFUzI1NiJ9.e30.", ecPEM)
	f.Add("eyJhbGciOiJub25lIn0.e30.", "secret")
	f.Add("..", "-----BEGIN PUBLIC KEY-----")
	f.Fuzz(func(t *testing.T, token, signer string) {
		claims, err := parseWithAllSigners(token, []string{signer}, nil)
		if err == nil && claims == nil {
			t.Fatalf("parseWithAllSigners accepted %q with nil claims", token)
		}
	})
}

func FuzzResolveStaticKey(f *testing.F) {
	f.Add(fuzzECPublicPEM(f), "ES256")
	f.Add("secret", "HS256")
	f.Add("secret", "RS256")
	f.Add("-----END PUBLIC KEY-----", "HS256")
	f.Fuzz(func(t *testing.T, signer, alg string) {
		key, err := resolveStaticKey(signer, alg)
		if err == nil && key == nil {
			t.Fatalf("resolveStaticKey(%q, %q) returned no error and no key", signer, alg)
		}
	})
}

func FuzzParsePrivateKey(f *testing.F) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		f.Fatal(err)
	}
	der, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}))
	f.Add([]byte("-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n"))
	f.Add([]byte("not pem"))
	f.Fuzz(func(t *testing.T, data []byte) {
		key, method, err := parsePrivateKey(data)
		if err == nil && (key == nil || method == nil) {
			t.Fatalf("parsePrivateKey returned no error but key=%v method=%v", key, method)
		}
	})
}

// fuzzECPublicPEM returns a PEM-encoded P-256 public key for seed inputs.
func fuzzECPublicPEM(f *testing.F) string {
	f.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		f.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&k.PublicKey)
	if err != nil {
		f.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}
