//go:build !no_sidecar

package sidecar

import (
	"net"
	"net/http"
	"strings"
)

// applyXFF sets X-Forwarded-For on the outgoing request according to the
// configured mode ("append" | "replace" | "strip"), then sets X-Real-IP.
// incomingXFF is the original XFF header from the inbound request (before any
// Rewrite stripping); clientAddr is r.RemoteAddr of the inbound request.
// trustedHops is the number of rightmost entries in the full chain that belong
// to trusted infrastructure (load balancers). X-Real-IP is set to the first
// entry to the left of the trusted zone.
func applyXFF(out *http.Request, incomingXFF, clientAddr, mode string, trustedHops int) {
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
	out.Header.Set("x-real-ip", realClientIP(incomingXFF, ip, trustedHops))
}

// realClientIP returns the real client IP given the inbound XFF chain, the
// socket peer IP, and the number of trusted hops.
//
// With trustedHops=0 the socket peer is returned (existing behaviour).
// With trustedHops>0, the full conceptual chain is built as:
//
//	[incomingXFF entries..., socketPeerIP]
//
// The real client is the entry immediately to the left of the trusted zone
// (i.e. chain[len-1-trustedHops]). If the chain is shorter than expected,
// the socket peer is returned as a safe fallback.
func realClientIP(incomingXFF, socketPeerIP string, trustedHops int) string {
	if trustedHops <= 0 {
		return socketPeerIP
	}
	var chain []string
	if incomingXFF != "" {
		for _, e := range strings.Split(incomingXFF, ",") {
			chain = append(chain, strings.TrimSpace(e))
		}
	}
	chain = append(chain, socketPeerIP)

	idx := len(chain) - 1 - trustedHops
	if idx < 0 {
		return socketPeerIP
	}
	return chain[idx]
}

func addrToIP(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}
