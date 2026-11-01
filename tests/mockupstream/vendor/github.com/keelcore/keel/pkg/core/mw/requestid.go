package mw

import (
	"context"
	"crypto/rand"
	"net/http"
	"time"

	"github.com/keelcore/keel/pkg/core/ctxkeys"
)

// RequestID reads the inbound X-Request-ID header; if absent or not a valid ID
// (see validRequestID), generates a ULID. The ID is stored in the request
// context and echoed on the response.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("x-request-id")
		if !validRequestID(id) {
			id = newULID()
		}
		ctx := context.WithValue(r.Context(), ctxkeys.RequestID, id)
		w.Header().Set("x-request-id", id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// crockford is the Crockford base32 alphabet (omits I, L, O, U).
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// newULID generates a 26-character Crockford base32 ULID:
// 10 chars for 48-bit millisecond timestamp + 16 chars for 80-bit random.
func newULID() string {
	var rnd [10]byte
	_, _ = rand.Read(rnd[:])

	ms := uint64(time.Now().UnixMilli())
	out := make([]byte, 26)

	// Time: 48 bits → 10 chars (5 bits each, MSB first).
	for i := 9; i >= 0; i-- {
		out[i] = crockford[ms&0x1F]
		ms >>= 5
	}

	// Random: 80 bits split into two 40-bit halves → 8 chars each.
	hi := uint64(rnd[0])<<32 | uint64(rnd[1])<<24 | uint64(rnd[2])<<16 | uint64(rnd[3])<<8 | uint64(rnd[4])
	lo := uint64(rnd[5])<<32 | uint64(rnd[6])<<24 | uint64(rnd[7])<<16 | uint64(rnd[8])<<8 | uint64(rnd[9])
	for i := 25; i >= 18; i-- {
		out[i] = crockford[lo&0x1F]
		lo >>= 5
	}
	for i := 17; i >= 10; i-- {
		out[i] = crockford[hi&0x1F]
		hi >>= 5
	}

	return string(out)
}

// maxRequestIDLen bounds a client-supplied request ID (UUIDs, ULIDs and W3C
// trace IDs all fit).
const maxRequestIDLen = 128

// validRequestID accepts 1..maxRequestIDLen bytes of [A-Za-z0-9._:-] so a
// client-supplied ID cannot inject log fields, control characters or header
// content when it is logged and echoed.
func validRequestID(id string) bool {
	if id == "" || len(id) > maxRequestIDLen {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-', c == '_', c == '.', c == ':':
		default:
			return false
		}
	}
	return true
}
