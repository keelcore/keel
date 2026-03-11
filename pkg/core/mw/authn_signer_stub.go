//go:build no_authn

package mw

import "net/http"

// JWTSigner is a no-op stub used when the no_authn build tag is active.
type JWTSigner struct{}

func NewJWTSigner(_, _ string) (*JWTSigner, error) { return &JWTSigner{}, nil }

func (s *JWTSigner) SignRequest(_ *http.Request) error { return nil }
