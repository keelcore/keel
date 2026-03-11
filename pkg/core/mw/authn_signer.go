//go:build !no_authn

package mw

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// JWTSigner mints short-lived JWTs for outbound keel-to-keel requests.
type JWTSigner struct {
	myID   string
	key    any
	method jwt.SigningMethod
}

// NewJWTSigner loads the private key at keyFile and returns a signer that
// identifies itself as myID in outbound JWT claims.
func NewJWTSigner(myID, keyFile string) (*JWTSigner, error) {
	pemData, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("read key file: %w", err)
	}
	key, method, err := parsePrivateKey(pemData)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	return &JWTSigner{myID: myID, key: key, method: method}, nil
}

// SignRequest mints a fresh 5-minute JWT and sets Authorization: Bearer on req.
func (s *JWTSigner) SignRequest(req *http.Request) error {
	now := time.Now()
	token := jwt.NewWithClaims(s.method, jwt.MapClaims{
		"iss": s.myID,
		"sub": s.myID,
		"iat": now.Unix(),
		"exp": now.Add(5 * time.Minute).Unix(),
	})
	signed, err := token.SignedString(s.key)
	if err != nil {
		return fmt.Errorf("sign jwt: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+signed)
	return nil
}

// parsePrivateKey decodes a PEM block and returns the private key and the
// appropriate JWT signing method. Tries PKCS#8, PKCS#1 RSA, and SEC1 EC.
func parsePrivateKey(pemData []byte) (any, jwt.SigningMethod, error) {
	block, _ := pem.Decode(pemData)
	if block == nil {
		return nil, nil, errors.New("no PEM block found")
	}

	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		return keyAndMethod(k)
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, jwt.SigningMethodRS256, nil
	}
	if k, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		method, err := ecSigningMethod(k)
		if err != nil {
			return nil, nil, err
		}
		return k, method, nil
	}
	return nil, nil, fmt.Errorf("unsupported private key type in PEM block %q", block.Type)
}

func keyAndMethod(key any) (any, jwt.SigningMethod, error) {
	switch k := key.(type) {
	case *rsa.PrivateKey:
		return k, jwt.SigningMethodRS256, nil
	case *ecdsa.PrivateKey:
		method, err := ecSigningMethod(k)
		return k, method, err
	default:
		return nil, nil, fmt.Errorf("unsupported PKCS8 key type %T", key)
	}
}

// ecSigningMethod maps an EC curve to its JWT signing method.
func ecSigningMethod(k *ecdsa.PrivateKey) (jwt.SigningMethod, error) {
	switch k.Curve {
	case elliptic.P256():
		return jwt.SigningMethodES256, nil
	case elliptic.P384():
		return jwt.SigningMethodES384, nil
	case elliptic.P521():
		return jwt.SigningMethodES512, nil
	default:
		return nil, fmt.Errorf("unsupported EC curve %s", k.Curve.Params().Name)
	}
}
