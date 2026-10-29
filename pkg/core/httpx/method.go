package httpx

import "net/http"

// MethodOther is the label for any request method outside the standard set.
const MethodOther = "OTHER"

// standardMethods are the RFC 9110 / RFC 5789 methods kept verbatim as labels.
var standardMethods = map[string]struct{}{
	http.MethodGet:     {},
	http.MethodHead:    {},
	http.MethodPost:    {},
	http.MethodPut:     {},
	http.MethodPatch:   {},
	http.MethodDelete:  {},
	http.MethodConnect: {},
	http.MethodOptions: {},
	http.MethodTrace:   {},
}

// MethodLabel bounds a client-supplied request method for use as a metric label
// or tag. net/http accepts any RFC 9110 token as a method, so labelling with the
// raw value lets a client mint unbounded series (memory DoS) and smuggle tag
// syntax; every non-standard method collapses to MethodOther.
func MethodLabel(method string) string {
	if _, ok := standardMethods[method]; ok {
		return method
	}
	return MethodOther
}
