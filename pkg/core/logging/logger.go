package logging

import (
    "encoding/json"
    "fmt"
    "os"
    "sync"
    "time"
)

type Config struct {
    JSON bool
}

type Logger struct {
    mu   sync.Mutex
    json bool
}

func New(cfg Config) *Logger {
    return &Logger{json: cfg.JSON}
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
        _, _ = fmt.Fprintln(os.Stdout, string(b))
        return
    }
    _, _ = fmt.Fprintf(os.Stdout, "%s [%s] %s %v\n", fields["ts"], level, msg, fields)
}
