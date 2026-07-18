package core

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFlush(t *testing.T) {
	// httptest.ResponseRecorder implements http.Flusher, so Flush succeeds directly.
	if err := Flush(httptest.NewRecorder()); err != nil {
		t.Errorf("Flush(recorder) = %v, want nil", err)
	}
	// Wrapped in a writer that implements Unwrap, Flush must traverse to the recorder's flusher.
	if err := Flush(unwrapWriter{httptest.NewRecorder()}); err != nil {
		t.Errorf("Flush(unwrappable wrapper) = %v, want nil (should traverse Unwrap)", err)
	}
	// A writer with no flusher and no Unwrap in its chain cannot flush.
	if err := Flush(opaqueWriter{httptest.NewRecorder()}); err == nil {
		t.Error("Flush of a non-flushable, non-unwrappable writer should error")
	}
}

// unwrapWriter wraps a writer and exposes it via Unwrap (keel's wrapper pattern), so
// http.ResponseController can reach the inner flusher.
type unwrapWriter struct{ inner http.ResponseWriter }

func (u unwrapWriter) Header() http.Header         { return u.inner.Header() }
func (u unwrapWriter) Write(b []byte) (int, error) { return u.inner.Write(b) }
func (u unwrapWriter) WriteHeader(code int)        { u.inner.WriteHeader(code) }
func (u unwrapWriter) Unwrap() http.ResponseWriter { return u.inner }

// opaqueWriter wraps a writer WITHOUT Unwrap or Flush, hiding the inner flusher.
type opaqueWriter struct{ inner http.ResponseWriter }

func (o opaqueWriter) Header() http.Header         { return o.inner.Header() }
func (o opaqueWriter) Write(b []byte) (int, error) { return o.inner.Write(b) }
func (o opaqueWriter) WriteHeader(code int)        { o.inner.WriteHeader(code) }
