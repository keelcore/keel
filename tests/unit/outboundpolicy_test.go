package unit

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"testing"
)

// errorTB records Errorf without failing the test.
type errorTB struct {
	testing.TB
	msg string
}

func (e *errorTB) Helper() {}

func (e *errorTB) Errorf(format string, args ...any) { e.msg = fmt.Sprintf(format, args...) }

func TestAssertOutboundPolicy(t *testing.T) {
	policy := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13}}}
	cases := []struct {
		name   string
		client *http.Client
		want   bool // an error reported
	}{
		{"policy", policy, false},
		{"not a transport", &http.Client{Transport: http.NewFileTransport(http.Dir("."))}, true},
		{"no TLS config", &http.Client{Transport: &http.Transport{}}, true},
		{"TLS 1.2", &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}}, true},
	}
	for _, c := range cases {
		e := &errorTB{}
		AssertOutboundPolicy(e, c.name, c.client)
		if got := e.msg != ""; got != c.want {
			t.Errorf("%s: error reported = %v (%q), want %v", c.name, got, e.msg, c.want)
		}
	}
}
