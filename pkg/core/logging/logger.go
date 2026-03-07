package logging

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

type Config struct {
	JSON bool
	Out  io.Writer // nil → os.Stdout
}

type Logger struct {
	mu   sync.Mutex
	json bool
	out  io.Writer
}

func New(cfg Config) *Logger {
	out := cfg.Out
	if out == nil {
		out = os.Stdout
	}
	return &Logger{json: cfg.JSON, out: out}
}

func (l *Logger) Info(msg string, fields map[string]any) { l.log("info", msg, fields) }
func (l *Logger) Warn(msg string, fields map[string]any) { l.log("warn", msg, fields) }

func (l *Logger) log(level, msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if fields == nil {
		fields = map[string]any{}
	}
	fields["ts"] = time.Now().UTC().Format(time.RFC3339Nano)
	fields["level"] = level
	fields["msg"] = msg

	if l.json {
		b, _ := json.Marshal(fields)
		_, _ = fmt.Fprintln(l.out, string(b))
		return
	}
	_, _ = fmt.Fprintf(l.out, "%s [%s] %s %v\n", fields["ts"], level, msg, fields)
}
