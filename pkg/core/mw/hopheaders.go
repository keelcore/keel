package mw

import (
	"net/http"
)

// HopHeaders applies keel's policy for the inbound headers that only the adjacent
// hop may create (docs/security.md, "Hop headers"). It runs before TraceContext
// and RequestID:
//
//   - a request with more than one Authorization field is refused (400): keel and
//     the upstream would otherwise read different credentials;
//   - a repeated traceparent, tracestate or x-request-id is dropped, so keel
//     generates its own value;
//   - without a trusted hop in front of keel (trustInbound false, i.e.
//     sidecar.xff_trusted_hops is 0), those headers come from the client and are
//     dropped too: keel starts a new trace and assigns the request ID.
func HopHeaders(trustInbound bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.Header.Values("Authorization")) > 1 {
			http.Error(w, "repeated Authorization header", http.StatusBadRequest)
			return
		}
		for _, name := range relayedIDHeaders {
			if !trustInbound || len(r.Header.Values(name)) > 1 {
				r.Header.Del(name)
			}
		}
		next.ServeHTTP(w, r)
	})
}

// relayedIDHeaders identify a request across hops; keel keeps them only from a
// trusted hop, and only when they appear once.
var relayedIDHeaders = []string{"traceparent", "tracestate", "x-request-id"}
