//go:build !no_sidecar

package sidecar

import (
	"net"
	"net/http"
)

// applyXFF sets X-Forwarded-For on the outgoing request according to the
// configured mode ("append" | "replace" | "strip"), then sets X-Real-IP.
// incomingXFF is the original XFF header from the inbound request (before any
// Rewrite stripping); clientAddr is r.RemoteAddr of the inbound request.
func applyXFF(out *http.Request, incomingXFF, clientAddr, mode string, _ int) {
	ip := addrToIP(clientAddr)
	switch mode {
	case "replace":
		out.Header.Set("x-forwarded-for", ip)
	case "strip":
		out.Header.Del("x-forwarded-for")
	default: // "append"
		if incomingXFF == "" {
			out.Header.Set("x-forwarded-for", ip)
		} else {
			out.Header.Set("x-forwarded-for", incomingXFF+", "+ip)
		}
	}
	out.Header.Set("x-real-ip", ip)
}

func addrToIP(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}