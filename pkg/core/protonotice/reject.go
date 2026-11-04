package protonotice

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/keelcore/keel/pkg/core/ctxkeys"
)

// NoticePath is where the human-readable notice is served, on the plain-HTTP listener.
const NoticePath = "/.well-known/keel/upgrade"

// NoticeURL is the notice page for code under base (scheme://host[:port]); "" without a base.
func NoticeURL(base string, code Code) string {
	if base == "" {
		return ""
	}
	return strings.TrimRight(base, "/") + NoticePath + "?code=" + string(code)
}

// WriteRejection answers r with code's rejection: 301 to the notice page under
// noticeBase, carrying the problem body so automation that does not follow the
// redirect still reads it; or, with no notice base, 426 with the problem in place.
// required, when given, replaces the code's default requirement (e.g. HTTP/3).
// The connection closes after the response.
func WriteRejection(w http.ResponseWriter, r *http.Request, code Code, noticeBase string, required ...string) {
	e, _ := Lookup(code)
	if len(required) > 0 {
		e.Required = required
	}
	requestID, _ := r.Context().Value(ctxkeys.RequestID).(string)
	status, body, location := rejection(e, noticeBase, requestID)
	h := w.Header()
	if location != "" {
		h.Set("Location", location)
	}
	h.Set("Cache-Control", "no-store")
	h.Set("Connection", "close")
	h.Set("Content-Type", ContentType)
	h.Set("Keel-Error-Code", string(code))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// WriteRawRejection writes code's rejection as a complete HTTP/1.1 response onto
// w, for a connection keel never parsed as HTTP (plain HTTP sent to the HTTPS port).
func WriteRawRejection(w io.Writer, code Code, noticeBase, requestID string) error {
	e, _ := Lookup(code)
	status, body, location := rejection(e, noticeBase, requestID)
	var b strings.Builder
	fmt.Fprintf(&b, "HTTP/1.1 %d %s\r\n", status, http.StatusText(status))
	if location != "" {
		fmt.Fprintf(&b, "Location: %s\r\n", location)
	}
	fmt.Fprintf(&b, "Cache-Control: no-store\r\nConnection: close\r\nContent-Type: %s\r\n", ContentType)
	fmt.Fprintf(&b, "Keel-Error-Code: %s\r\nContent-Length: %d\r\n\r\n", code, len(body))
	b.Write(body)
	_, err := io.WriteString(w, b.String())
	return err
}

// rejection is the status, problem body and Location for e.
func rejection(e Entry, noticeBase, requestID string) (int, []byte, string) {
	location := NoticeURL(noticeBase, e.Code)
	p := e.Problem(location, requestID)
	if location == "" {
		p.Status = http.StatusUpgradeRequired
	}
	body, _ := json.Marshal(p)
	return p.Status, body, location
}
