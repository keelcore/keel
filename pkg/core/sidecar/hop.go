//go:build !no_sidecar

package sidecar

import (
	"fmt"
	"net/http"

	"github.com/keelcore/keel/pkg/core/ctxkeys"
)

// applyHopIdentity sets the headers that identify this hop on the outbound
// request: keel's own request ID and trace context (mw.RequestID and
// mw.TraceContext put them in the request context, from a trusted hop's values or
// newly generated), and keel's entry appended to Via (RFC 9110 §7.6.3). Entries
// already in Via are the earlier hops' claims; keel's own entry is always last.
func applyHopIdentity(out, in *http.Request, via string) {
	ctx := in.Context()
	if id, ok := ctx.Value(ctxkeys.RequestID).(string); ok && id != "" {
		out.Header.Set("x-request-id", id)
	}
	traceID, _ := ctx.Value(ctxkeys.TraceID).(string)
	spanID, _ := ctx.Value(ctxkeys.SpanID).(string)
	if traceID != "" && spanID != "" {
		out.Header.Set("traceparent", fmt.Sprintf("00-%s-%s-01", traceID, spanID))
	}
	// The outbound headers start as a copy of the inbound ones, earlier Via lines included.
	out.Header.Add("Via", viaProtocol(in)+" "+via)
}

// viaProtocol is the received-protocol of a Via entry: "1.1", "2", "3".
func viaProtocol(r *http.Request) string {
	if r.ProtoMajor >= 2 {
		return fmt.Sprint(r.ProtoMajor)
	}
	return fmt.Sprintf("%d.%d", r.ProtoMajor, r.ProtoMinor)
}

// viaPseudonym is keel's received-by in Via: authn.my_id when it is a valid
// header token, else "keel".
func viaPseudonym(myID string) string {
	if myID == "" || len(myID) > 64 {
		return "keel"
	}
	for i := 0; i < len(myID); i++ {
		c := myID[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-', c == '_', c == '.':
		default:
			return "keel"
		}
	}
	return myID
}
