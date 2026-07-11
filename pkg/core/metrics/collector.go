package metrics

import "io"

// Collector renders additional Prometheus text-format series into the /metrics
// exposition. An embedder registers collectors (via Server.Metrics().Register)
// so its own series render alongside keel's built-in metrics, through the same
// responder — no second metrics endpoint or instrumentation stack.
//
// Collect MUST emit valid Prometheus text-format 0.0.4 (each series preceded by
// its own # HELP / # TYPE lines) and MUST NOT block; it is called under the
// metrics read path when /metrics is scraped.
type Collector interface {
	Collect(w io.Writer)
}
