package httpx

import (
	"net/http"
	"testing"
)

// Standard methods are kept verbatim.
func TestMethodLabel_StandardMethodsVerbatim(t *testing.T) {
	for _, m := range []string{
		http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace,
	} {
		if got := MethodLabel(m); got != m {
			t.Errorf("MethodLabel(%q) = %q, want %q", m, got, m)
		}
	}
}

// Arbitrary, lowercase, and tag-smuggling methods collapse to OTHER.
func TestMethodLabel_NonStandardCollapses(t *testing.T) {
	for _, m := range []string{"AAAA1", "get", "GET|#x", "PROPFIND", ""} {
		if got := MethodLabel(m); got != MethodOther {
			t.Errorf("MethodLabel(%q) = %q, want %q", m, got, MethodOther)
		}
	}
}
