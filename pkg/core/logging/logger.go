// Package logging provides the structured logger used throughout keel.
//
// # The early-boot logging problem
//
// Logging has a chicken-and-egg problem: the user's log configuration (level,
// JSON format, remote sink) lives in the config file, but the config file must
// itself be loaded and parsed — a process that needs a working logger to report
// errors. The Linux kernel faces the same issue: earlycon/printk emit to a
// hard-wired serial port long before the kernel's logging subsystem is
// initialised; once the full driver stack is up, the kernel switches to the
// configured console.
//
// Keel follows the same pattern across three phases:
//
//	Phase 1 — bootstrap (earlycon equivalent):
//	  logging.New(logging.Config{JSON: true}) creates a logger at "info" level
//	  writing to stdout. This is called before any user configuration is known.
//	  All CLI flag parsing and config load errors are emitted through this
//	  logger. It is unconditional — it never fails.
//
//	Phase 2 — reconfigure (driver handoff):
//	  Once the config file is loaded and validated, Server.Run calls
//	  Logger.Reconfigure with the user's level, JSON flag, and (if a remote
//	  sink is configured) a new io.MultiWriter(stdout, remoteSink). From this
//	  point forward the user's settings are in effect. Logs emitted during
//	  Phase 1 are never lost — they went to stdout before reconfiguration.
//
//	Phase 3 — live reload (SIGHUP / POST /admin/reload):
//	  Server.Reload calls Reconfigure again with the updated config. Level and
//	  JSON flag change atomically. The output writer is preserved (cfg.Out is
//	  nil on reload calls) — the remote-sink goroutine is already running and
//	  must not be re-initialised mid-flight.
//
// Reconfigure is concurrency-safe: the level field is updated via sync/atomic
// (so filtering decisions are lock-free in the hot path) and the json/out
// fields are updated under a mutex.
package logging

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Level ordinals — higher value means less verbose.
const (
	levelDebug int32 = iota
	levelInfo
	levelWarn
	levelError
)

// ParseLevel converts a level name ("debug", "info", "warn", "error") to its
// internal ordinal. Case-insensitive. An empty string maps to "info".
// Returns levelInfo and a non-nil error for unknown names.
func ParseLevel(s string) (int32, error) {
	switch strings.ToLower(s) {
	case "debug":
		return levelDebug, nil
	case "info", "":
		return levelInfo, nil
	case "warn":
		return levelWarn, nil
	case "error":
		return levelError, nil
	default:
		return levelInfo, fmt.Errorf("unknown log level %q", s)
	}
}

// Config carries the options accepted by New and Reconfigure.
type Config struct {
	JSON  bool
	Level string    // "debug", "info", "warn", "error" (default "info")
	Out   io.Writer // nil → os.Stdout; ignored by Reconfigure when nil
}

// Logger is a minimal structured logger. All methods are safe for concurrent use.
type Logger struct {
	mu     sync.Mutex
	json   bool
	out    io.Writer
	level  int32     // read/written via sync/atomic
	ExitFn func(int) // injectable; defaults to os.Exit
}

// New constructs a Logger from cfg. An unknown Level defaults to "info".
func New(cfg Config) *Logger {
	out := cfg.Out
	if out == nil {
		out = os.Stdout
	}
	lvl, _ := ParseLevel(cfg.Level)
	return &Logger{json: cfg.JSON, out: out, level: lvl, ExitFn: os.Exit}
}

// Reconfigure atomically applies a new level and JSON flag.
// If cfg.Out is non-nil, the output writer is also replaced.
// Returns an error if cfg.Level is unrecognised; the previous level is preserved.
func (l *Logger) Reconfigure(cfg Config) error {
	lvl, err := ParseLevel(cfg.Level)
	if err != nil {
		return err
	}
	atomic.StoreInt32(&l.level, lvl)
	l.mu.Lock()
	l.json = cfg.JSON
	if cfg.Out != nil {
		l.out = cfg.Out
	}
	l.mu.Unlock()
	return nil
}

func (l *Logger) Debug(msg string, fields map[string]any) { l.log("debug", levelDebug, msg, fields) }
func (l *Logger) Info(msg string, fields map[string]any)  { l.log("info", levelInfo, msg, fields) }
func (l *Logger) Warn(msg string, fields map[string]any)  { l.log("warn", levelWarn, msg, fields) }
func (l *Logger) Error(msg string, fields map[string]any) { l.log("error", levelError, msg, fields) }

// Exit always logs (bypasses level filter) then terminates the process cleanly.
func (l *Logger) Exit(msg string, fields map[string]any) {
	l.write("info", msg, fields)
	l.ExitFn(0)
}

// Fatal always logs (bypasses level filter) then terminates the process.
func (l *Logger) Fatal(msg string, fields map[string]any) {
	l.write("error", msg, fields)
	l.ExitFn(1)
}

// log emits msg at the given level if the current level filter allows it.
func (l *Logger) log(levelName string, lvl int32, msg string, fields map[string]any) {
	if atomic.LoadInt32(&l.level) > lvl {
		return
	}
	l.write(levelName, msg, fields)
}

// write formats and emits a log entry unconditionally (no level filter).
func (l *Logger) write(levelName, msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if fields == nil {
		fields = map[string]any{}
	}
	fields["ts"] = time.Now().UTC().Format(time.RFC3339Nano)
	fields["level"] = levelName
	fields["msg"] = msg

	if l.json {
		b, _ := json.Marshal(fields)
		_, _ = fmt.Fprintln(l.out, string(b))
		return
	}
	_, _ = fmt.Fprintf(l.out, "%s [%s] %s %v\n", fields["ts"], levelName, msg, fields)
}
