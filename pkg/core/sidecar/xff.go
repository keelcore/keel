//go:build !no_sidecar

package sidecar

import (
	"net"
	"net/http"
	"strings"
)

// applyXFF sets X-Forwarded-For on the outgoing request according to the
// configured mode ("append" | "replace" | "strip"), then sets X-Real-IP.
// incomingXFF is the original XFF chain from the inbound request (every field
// line, comma-joined, before any Rewrite stripping); clientAddr is r.RemoteAddr
// of the inbound request. trustedHops is the number of rightmost entries in the
// full chain that belong to trusted infrastructure (load balancers). X-Real-IP is
// set to the first entry to the left of the trusted zone.
//
// Every incoming entry is normalized (xffEntry) before keel forwards or reads it,
// so a client-supplied entry can only ever occupy one position in the chain.
func applyXFF(out *http.Request, incomingXFF, clientAddr, mode string, trustedHops int) {
	ip := addrToIP(clientAddr)
	chain := xffChain(incomingXFF)
	switch mode {
	case "replace":
		out.Header.Set("x-forwarded-for", ip)
	case "strip":
		out.Header.Del("x-forwarded-for")
	default: // "append"
		out.Header.Set("x-forwarded-for", strings.Join(append(chain, ip), ", "))
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
// (i.e. chain[len-1-trustedHops]). If the chain is shorter than expected, or
// that entry is not an IP address, the socket peer is returned as a safe fallback.
func realClientIP(incomingXFF, socketPeerIP string, trustedHops int) string {
	if trustedHops <= 0 {
		return socketPeerIP
	}
	chain := append(xffChain(incomingXFF), socketPeerIP)

	idx := len(chain) - 1 - trustedHops
	if idx < 0 || chain[idx] == xffUnknown {
		return socketPeerIP
	}
	return chain[idx]
}

// xffUnknown stands in for an X-Forwarded-For entry that is not an IP address.
const xffUnknown = "unknown"

// xffChain splits an X-Forwarded-For value into its normalized entries.
func xffChain(xff string) []string {
	if strings.TrimSpace(xff) == "" {
		return nil
	}
	parts := strings.Split(xff, ",")
	chain := make([]string, 0, len(parts))
	for _, e := range parts {
		chain = append(chain, xffEntry(e))
	}
	return chain
}

// xffEntry is one X-Forwarded-For entry as an IP address in canonical form (an
// optional port and IPv6 brackets removed), or xffUnknown for anything else:
// empty elements, hostnames, junk, or text carrying another delimiter.
func xffEntry(e string) string {
	e = strings.TrimSpace(e)
	if host, _, err := net.SplitHostPort(e); err == nil {
		e = host
	}
	e = strings.TrimSuffix(strings.TrimPrefix(e, "["), "]")
	ip := net.ParseIP(e)
	if ip == nil {
		return xffUnknown
	}
	return ip.String()
}

func addrToIP(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}
