package tls

import (
	"crypto/tls"
	"errors"
	"sync/atomic"
)

// CertLoader holds a TLS certificate that can be reloaded atomically without
// interrupting in-flight TLS sessions. Use Get as tls.Config.GetCertificate
// so each new handshake picks up the most recently loaded certificate.
type CertLoader struct {
	cert atomic.Pointer[tls.Certificate]
}

// NewCertLoader loads the initial certificate from certFile and keyFile.
func NewCertLoader(certFile, keyFile string) (*CertLoader, error) {
	l := &CertLoader{}
	return l, l.Reload(certFile, keyFile)
}

// Reload replaces the stored certificate from disk. If loading fails the
// previous certificate remains active and the error is returned.
func (l *CertLoader) Reload(certFile, keyFile string) error {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return err
	}
	l.cert.Store(&cert)
	return nil
}

// Get implements tls.Config.GetCertificate; called once per TLS handshake.
func (l *CertLoader) Get(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	c := l.cert.Load()
	if c == nil {
		return nil, errors.New("no certificate loaded")
	}
	return c, nil
}
