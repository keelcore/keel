// pkg/core/log_file.go
package core

import (
	"io"
	"os"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
)

// applyFileSink brings the logging.file sink in line with cfg and returns the writer to attach
// (nil: none) and any sink it replaced, for the caller to close once the logger has been switched
// off it. The same path keeps its open file and takes the new rotation policy. A path that cannot
// be opened is warned about, and logging carries on without it. Caller holds sinkMu.
func (s *Server) applyFileSink(cfg config.Config) (io.Writer, *logging.RotatingFile) {
	policy := rotationPolicy(cfg.Logging.Rotation)
	if s.fileSink != nil && s.fileSinkPath == cfg.Logging.File {
		s.fileSink.SetPolicy(policy)
		return s.fileSink, nil
	}
	prev := s.fileSink
	s.fileSink, s.fileSinkPath = nil, ""
	if cfg.Logging.File == "" {
		return nil, prev
	}
	f, err := logging.NewRotatingFile(cfg.Logging.File, policy)
	if err != nil {
		s.logger.Warn("log_file_open_failed", map[string]any{"path": cfg.Logging.File, "err": err.Error()})
		return nil, prev
	}
	s.fileSink, s.fileSinkPath = f, cfg.Logging.File
	return f, prev
}

func rotationPolicy(r config.LogRotationConfig) logging.RotationPolicy {
	return logging.RotationPolicy{MaxSizeMB: r.MaxSizeMB, MaxAgeDays: r.MaxAgeDays, MaxBackups: r.MaxBackups, Compress: r.Compress}
}

// logWriters joins stdout with the sinks attached (nil entries skipped).
func logWriters(sinks ...io.Writer) io.Writer {
	writers := []io.Writer{os.Stdout}
	for _, w := range sinks {
		if w != nil {
			writers = append(writers, w)
		}
	}
	return io.MultiWriter(writers...)
}

// rotateLogs rotates the file sink now (SIGUSR2).
func (s *Server) rotateLogs() {
	s.sinkMu.Lock()
	f, path := s.fileSink, s.fileSinkPath
	s.sinkMu.Unlock()
	if f == nil {
		s.logger.Info("log_rotation_no_file_sink", nil)
		return
	}
	if err := f.Rotate(); err != nil {
		s.logger.Warn("log_rotation_failed", map[string]any{"err": err.Error()})
		return
	}
	s.logger.Info("log_rotated", map[string]any{"path": path})
}

// closeFileSink waits for background compression and closes the file, at shutdown. Lines logged
// after it reach stdout only.
func (s *Server) closeFileSink() {
	s.sinkMu.Lock()
	defer s.sinkMu.Unlock()
	if s.fileSink != nil {
		_ = s.fileSink.Close()
		s.fileSink, s.fileSinkPath = nil, ""
	}
}
