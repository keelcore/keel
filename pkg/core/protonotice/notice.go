package protonotice

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"strings"
)

// NoticeOptions are the operator's optional strings on the notice page.
type NoticeOptions struct {
	HelpURL string // tls.notice.help_url
	Contact string // tls.notice.contact
}

// noticeHTML is the notice page: catalog text and the operator's strings, each HTML-escaped.
func noticeHTML(e Entry, opts NoticeOptions) string {
	var b strings.Builder
	esc := html.EscapeString
	fmt.Fprintf(&b, "<!doctype html>\n<html lang=\"en\"><head><meta charset=\"utf-8\"><title>%s</title></head>\n", esc(e.Title))
	fmt.Fprintf(&b, "<body><h1>%s</h1><p>%s</p>\n<p>Required: %s.</p>\n", esc(e.Title), esc(e.Detail), esc(strings.Join(e.Required, ", ")))
	if opts.HelpURL != "" {
		fmt.Fprintf(&b, "<p><a href=\"%s\">How to upgrade</a></p>\n", esc(opts.HelpURL))
	}
	if opts.Contact != "" {
		fmt.Fprintf(&b, "<p>Contact: %s</p>\n", esc(opts.Contact))
	}
	fmt.Fprintf(&b, "<p>Code: %s</p></body></html>\n", esc(string(e.Code)))
	return b.String()
}

// NoticeHandler serves the notice page for ?code=: HTML to browsers, the problem
// JSON to automation (Accept: application/json or problem+json, or ?format=json),
// plain text otherwise. The page is built from the code's catalog entry and the
// operator's options only; an unknown or missing code is 404. served (optional)
// is told each page served, by code and format.
func NoticeHandler(opts NoticeOptions, served func(Code, string)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e, ok := Lookup(Code(r.URL.Query().Get("code")))
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		format := noticeFormat(r)
		switch format {
		case "json":
			p := e.Problem("", "")
			p.Status = 0
			w.Header().Set("Content-Type", ContentType)
			_ = json.NewEncoder(w).Encode(p)
		case "html":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, noticeHTML(e, opts))
		default:
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprintf(w, "%s\n\n%s\nRequired: %s.\n", e.Title, e.Detail, strings.Join(e.Required, ", "))
			if opts.HelpURL != "" {
				fmt.Fprintf(w, "How to upgrade: %s\n", opts.HelpURL)
			}
			if opts.Contact != "" {
				fmt.Fprintf(w, "Contact: %s\n", opts.Contact)
			}
			fmt.Fprintf(w, "Code: %s\n", e.Code)
		}
		if served != nil {
			served(e.Code, format)
		}
	})
}

func noticeFormat(r *http.Request) string {
	if r.URL.Query().Get("format") == "json" {
		return "json"
	}
	accept := r.Header.Get("Accept")
	switch {
	case strings.Contains(accept, "application/json"), strings.Contains(accept, ContentType):
		return "json"
	case strings.Contains(accept, "text/html"):
		return "html"
	}
	return "text"
}
