//go:build !no_otel

// Package tracing implements a lightweight OTLP/HTTP span exporter using only
// the standard library. No OTel SDK packages are required. The exporter
// serialises spans to OTLP JSON (opentelemetry-proto JSON encoding) and POSTs
// them to a collector's /v1/traces endpoint in background batches.
// provider_stub.go provides no-op replacements when compiled with no_otel.
package tracing

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/keelcore/keel/pkg/config"
)

const (
	batchMax      = 512
	flushInterval = 5 * time.Second
	sendTimeout   = 10 * time.Second
	chanCap       = 1024
)

// Span is the data captured per request by the OTelSpan middleware.
type Span struct {
	TraceID      string // 32 lowercase hex chars
	SpanID       string // 16 lowercase hex chars
	ParentSpanID string // 16 lowercase hex chars; empty for root spans
	Name         string // e.g. "GET /healthz"
	Start        time.Time
	End          time.Time
	HTTPMethod   string
	HTTPPath     string
	HTTPStatus   int
}

// Exporter batches spans and POSTs them to an OTLP/HTTP collector endpoint.
type Exporter struct {
	url    string
	client *http.Client
	ch     chan Span
	done   chan struct{}
	wg     sync.WaitGroup
}

// Setup creates an Exporter from cfg and starts its background flush goroutine.
// Returns nil, nil when tracing is disabled or the endpoint is empty.
// The caller must call Shutdown to flush and stop the goroutine.
func Setup(cfg config.OTLPConfig) (*Exporter, error) {
	if !cfg.Enabled || cfg.Endpoint == "" {
		return nil, nil
	}

	scheme := "https://"
	if cfg.Insecure {
		scheme = "http://"
	}
	ep := strings.TrimPrefix(cfg.Endpoint, "https://")
	ep = strings.TrimPrefix(ep, "http://")

	e := &Exporter{
		url:    scheme + ep + "/v1/traces",
		client: &http.Client{Timeout: sendTimeout},
		ch:     make(chan Span, chanCap),
		done:   make(chan struct{}),
	}
	e.wg.Add(1)
	go e.run()
	return e, nil
}

// Submit enqueues s for export. Non-blocking: drops the span if the channel
// is full rather than stalling the request path.
func (e *Exporter) Submit(s Span) {
	select {
	case e.ch <- s:
	default:
	}
}

// Shutdown signals the background goroutine to stop, drains any buffered spans,
// and waits for the final flush to complete. Safe to call with a nil Exporter.
func Shutdown(e *Exporter) {
	if e == nil {
		return
	}
	close(e.done)
	e.wg.Wait()
}

func (e *Exporter) run() {
	defer e.wg.Done()
	batch := make([]Span, 0, batchMax)
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	flush := func() {
		if len(batch) > 0 {
			e.send(batch)
			batch = batch[:0]
		}
	}

	for {
		select {
		case s := <-e.ch:
			batch = append(batch, s)
			if len(batch) >= batchMax {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-e.done:
			for {
				select {
				case s := <-e.ch:
					batch = append(batch, s)
				default:
					flush()
					return
				}
			}
		}
	}
}

func (e *Exporter) send(spans []Span) {
	body, err := json.Marshal(buildRequest(spans))
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

// --- OTLP JSON types (opentelemetry-proto JSON encoding) ---
// Numeric uint64 fields are decimal strings per the proto3 JSON spec.

type exportReq struct {
	ResourceSpans []resourceSpan `json:"resourceSpans"`
}

type resourceSpan struct {
	Resource   otlpResource `json:"resource"`
	ScopeSpans []scopeSpan  `json:"scopeSpans"`
}

type otlpResource struct {
	Attributes []kv `json:"attributes"`
}

type scopeSpan struct {
	Scope otlpScope  `json:"scope"`
	Spans []otlpSpan `json:"spans"`
}

type otlpScope struct {
	Name string `json:"name"`
}

type otlpSpan struct {
	TraceID           string     `json:"traceId"`
	SpanID            string     `json:"spanId"`
	ParentSpanID      string     `json:"parentSpanId,omitempty"`
	Name              string     `json:"name"`
	Kind              int        `json:"kind"`
	StartTimeUnixNano string     `json:"startTimeUnixNano"`
	EndTimeUnixNano   string     `json:"endTimeUnixNano"`
	Attributes        []kv       `json:"attributes"`
	Status            otlpStatus `json:"status"`
}

type kv struct {
	Key   string  `json:"key"`
	Value kvValue `json:"value"`
}

type kvValue struct {
	StringValue *string `json:"stringValue,omitempty"`
	IntValue    *string `json:"intValue,omitempty"` // uint64 as decimal string
}

type otlpStatus struct {
	Code int `json:"code"` // 1=OK  2=ERROR
}

func strVal(s string) kvValue { return kvValue{StringValue: &s} }
func intVal(n int) kvValue    { v := strconv.Itoa(n); return kvValue{IntValue: &v} }

func buildRequest(spans []Span) exportReq {
	js := make([]otlpSpan, len(spans))
	for i, s := range spans {
		code := 1 // OK
		if s.HTTPStatus >= 500 {
			code = 2 // ERROR
		}
		js[i] = otlpSpan{
			TraceID:           s.TraceID,
			SpanID:            s.SpanID,
			ParentSpanID:      s.ParentSpanID,
			Name:              s.Name,
			Kind:              2, // SERVER
			StartTimeUnixNano: strconv.FormatInt(s.Start.UnixNano(), 10),
			EndTimeUnixNano:   strconv.FormatInt(s.End.UnixNano(), 10),
			Attributes: []kv{
				{Key: "http.request.method", Value: strVal(s.HTTPMethod)},
				{Key: "url.path", Value: strVal(s.HTTPPath)},
				{Key: "http.response.status_code", Value: intVal(s.HTTPStatus)},
			},
			Status: otlpStatus{Code: code},
		}
	}
	svcName := "keel"
	return exportReq{ResourceSpans: []resourceSpan{{
		Resource: otlpResource{Attributes: []kv{{Key: "service.name", Value: strVal(svcName)}}},
		ScopeSpans: []scopeSpan{{
			Scope: otlpScope{Name: "github.com/keelcore/keel"},
			Spans: js,
		}},
	}}}
}
