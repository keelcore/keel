package mw

import (
	"net/http/httptest"
	"testing"
)

// limitedResponseWriter.Unwrap exposes the wrapped writer.
func TestLimitedResponseWriter_Unwrap(t *testing.T) {
	rr := httptest.NewRecorder()
	lw := &limitedResponseWriter{ResponseWriter: rr, remaining: 5}
	if lw.Unwrap() != rr {
		t.Error("expected Unwrap to return the wrapped ResponseWriter")
	}
}

// limitedResponseWriter truncates writes beyond the remaining quota.
func TestLimitedResponseWriter_Truncates(t *testing.T) {
	rr := httptest.NewRecorder()
	lw := &limitedResponseWriter{ResponseWriter: rr, remaining: 5}

	n, err := lw.Write([]byte("hello world"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Errorf("expected 5 bytes written, got %d", n)
	}
	if rr.Body.String() != "hello" {
		t.Errorf("expected body 'hello', got %q", rr.Body.String())
	}
}

// limitedResponseWriter allows writes within quota.
func TestLimitedResponseWriter_WithinQuota(t *testing.T) {
	rr := httptest.NewRecorder()
	lw := &limitedResponseWriter{ResponseWriter: rr, remaining: 100}

	n, err := lw.Write([]byte("hi"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 bytes, got %d", n)
	}
}

// limitedResponseWriter with remaining=0 writes nothing.
func TestLimitedResponseWriter_ZeroRemaining_WritesNothing(t *testing.T) {
	rr := httptest.NewRecorder()
	lw := &limitedResponseWriter{ResponseWriter: rr, remaining: 0}

	n, err := lw.Write([]byte("data"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 bytes written, got %d", n)
	}
}
