package ctxkeys

type contextKey int

const (
	RequestID contextKey = iota
	TraceID
	SpanID
)
