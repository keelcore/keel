// Package protonotice is the catalog of protocol rejections keel reports to
// clients and operators: a stable code per rejection class, the requirement the
// client must meet, and the RFC 9457 problem body keel sends when it can still
// speak HTTP. A code never changes meaning; a new reason gets a new code.
package protonotice

import (
	"net/http"
	"strings"
)

// Code identifies one rejection class (KEEL-PROTO-NNN).
type Code string

const (
	TLSVersion          Code = "KEEL-PROTO-001"
	NoSharedALPN        Code = "KEEL-PROTO-002"
	NoSharedGroup       Code = "KEEL-PROTO-003"
	NoSharedCipher      Code = "KEEL-PROTO-004"
	ClientCertRequired  Code = "KEEL-PROTO-005"
	PlaintextOnHTTPS    Code = "KEEL-PROTO-006"
	HTTPVersionBelowMin Code = "KEEL-PROTO-007"
)

// ContentType is the media type of a rejection body (RFC 9457).
const ContentType = "application/problem+json"

// typeBase is where each code is documented; a problem's type is this plus the code's anchor.
const typeBase = "https://github.com/keelcore/keel/blob/main/docs/errors.md#"

// Entry is one code's meaning. Status is the HTTP status of the in-protocol
// rejection, 0 for a code that is only logged and counted (no HTTP channel).
type Entry struct {
	Code     Code
	Reason   string
	Status   int
	Title    string
	Detail   string
	Required []string
}

var catalog = []Entry{
	{TLSVersion, "tls_version_below_1_3", http.StatusMovedPermanently,
		"TLS 1.3 required", "This service requires TLS 1.3. Upgrade your client.", []string{"TLS 1.3"}},
	{NoSharedALPN, "no_shared_alpn", 0,
		"No common application protocol", "This service speaks HTTP/2 and HTTP/1.1. Upgrade your client.", []string{"h2", "http/1.1"}},
	{NoSharedGroup, "no_shared_group", 0,
		"No common key exchange group", "This service requires the P-256 or P-384 key exchange. Upgrade your client.", []string{"P-256", "P-384"}},
	{NoSharedCipher, "no_shared_cipher", 0,
		"No common cipher suite", "This service requires an AES-GCM TLS 1.3 cipher suite. Upgrade your client.",
		[]string{"TLS_AES_128_GCM_SHA256", "TLS_AES_256_GCM_SHA384"}},
	{ClientCertRequired, "client_certificate_required", http.StatusMovedPermanently,
		"Client certificate required", "This service requires a client certificate. Present one with your request.",
		[]string{"client certificate"}},
	{PlaintextOnHTTPS, "plaintext_on_https_port", http.StatusMovedPermanently,
		"HTTPS required", "This port speaks HTTPS only. Use an https:// URL.", []string{"https"}},
	{HTTPVersionBelowMin, "http_version_below_minimum", http.StatusMovedPermanently,
		"Newer HTTP version required", "This service requires a newer HTTP version. Upgrade your client.", []string{"HTTP/2"}},
}

// Codes lists every code, in order.
func Codes() []Code {
	out := make([]Code, len(catalog))
	for i, e := range catalog {
		out[i] = e.Code
	}
	return out
}

// Lookup is the entry for code.
func Lookup(code Code) (Entry, bool) {
	for _, e := range catalog {
		if e.Code == code {
			return e, true
		}
	}
	return Entry{}, false
}

// Problem is an RFC 9457 problem body with keel's members as extensions.
type Problem struct {
	Type      string   `json:"type"`
	Title     string   `json:"title"`
	Status    int      `json:"status,omitempty"`
	Detail    string   `json:"detail"`
	Code      Code     `json:"code"`
	Reason    string   `json:"reason"`
	Required  []string `json:"required"`
	NoticeURL string   `json:"notice_url,omitempty"`
	RequestID string   `json:"request_id,omitempty"`
}

// Problem is e's body, pointing at noticeURL (the human-readable notice) and
// carrying requestID when the request has one.
func (e Entry) Problem(noticeURL, requestID string) Problem {
	return Problem{
		Type:      typeBase + strings.ToLower(string(e.Code)),
		Title:     e.Title,
		Status:    e.Status,
		Detail:    e.Detail,
		Code:      e.Code,
		Reason:    e.Reason,
		Required:  e.Required,
		NoticeURL: noticeURL,
		RequestID: requestID,
	}
}
