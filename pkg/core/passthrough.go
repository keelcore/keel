package core

import "net/http"

// Flush pushes any buffered response bytes to the client immediately. It is keel's
// streaming-passthrough surface: a handler relaying an upstream stream (SSE or chunked) calls Flush
// after writing each event so the client receives it live rather than at end-of-response.
//
// keel's middleware chain wraps the http.ResponseWriter (metrics, access log, OWASP body limit,
// OTel span), and those wrappers are not themselves http.Flushers — so a handler MUST NOT depend on
// a direct w.(http.Flusher) assertion. Flush goes through http.ResponseController, which traverses
// each wrapper's Unwrap to reach the connection's flusher. It returns an error only when the
// underlying transport genuinely cannot flush (e.g. a writer with no flusher in its chain).
func Flush(w http.ResponseWriter) error {
	return http.NewResponseController(w).Flush()
}
