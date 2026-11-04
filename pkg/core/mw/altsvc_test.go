package mw

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

// U7: HTTPS responses advertise HTTP/3 so h3-capable clients upgrade on the next request.
func TestAltSvc(t *testing.T) {
	h := AltSvc(8443, 86400, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	for _, tc := range []struct {
		name  string
		tls   bool
		major int
		want  string
	}{
		{"https h1", true, 1, `h3=":8443"; ma=86400`},
		{"https h2", true, 2, `h3=":8443"; ma=86400`},
		{"already h3", true, 3, ""},
		{"plain http", false, 1, ""},
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.ProtoMajor = tc.major
		if tc.tls {
			r.TLS = &tls.ConnectionState{}
		} else {
			r.TLS = nil
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if got := rec.Header().Get("Alt-Svc"); got != tc.want {
			t.Errorf("%s: Alt-Svc %q, want %q", tc.name, got, tc.want)
		}
	}
}
