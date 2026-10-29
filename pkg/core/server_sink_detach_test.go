package core

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
)

// closableSink records writes and Close, standing in for the syslog writer.
type closableSink struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	closed bool
}

func (c *closableSink) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

func (c *closableSink) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *closableSink) state() (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Len(), c.closed
}

// newSinkServer returns a server whose buildRemoteSinkFn hands out sinks in order
// (nil entry = init error).
func newSinkServer(t *testing.T, sinks ...*closableSink) *Server {
	t.Helper()
	orig := buildRemoteSinkFn
	t.Cleanup(func() { buildRemoteSinkFn = orig })
	i := 0
	buildRemoteSinkFn = func(config.RemoteSinkConfig) (io.Writer, *logging.HTTPSink, error) {
		s := sinks[i]
		i++
		if s == nil {
			return nil, nil, errors.New("sink init boom")
		}
		return s, nil, nil
	}
	s := NewServer(logging.New(logging.Config{Out: io.Discard}), config.Config{})
	s.runCtx = context.Background()
	return s
}

func sinkCfg(enabled bool) config.Config {
	var cfg config.Config
	cfg.Logging.Level = "info"
	cfg.Logging.RemoteSink.Enabled = enabled
	cfg.Logging.RemoteSink.Endpoint = "syslog.example:514"
	cfg.Logging.RemoteSink.Protocol = "syslog"
	return cfg
}

// Disabling the remote sink on reload detaches it from the logger (logs stop
// reaching it) and closes it (no leaked connection).
func TestApplyRemoteSink_DisableDetachesAndCloses(t *testing.T) {
	sink := &closableSink{}
	s := newSinkServer(t, sink)
	s.applyRemoteSink(sinkCfg(true))
	s.logger.Info("while_attached", nil)
	before, _ := sink.state()
	if before == 0 {
		t.Fatal("sink received nothing while attached")
	}

	s.applyRemoteSink(sinkCfg(false))
	s.logger.Info("after_disable", nil)
	after, closed := sink.state()
	if after != before {
		t.Errorf("sink still receives logs after being disabled (%d → %d bytes)", before, after)
	}
	if !closed {
		t.Error("disabled sink was not closed")
	}
}

// Replacing the sink (SIGHUP with a new endpoint) closes the old one.
func TestApplyRemoteSink_ReplaceClosesOld(t *testing.T) {
	oldSink, newSink := &closableSink{}, &closableSink{}
	s := newSinkServer(t, oldSink, newSink)
	s.applyRemoteSink(sinkCfg(true))
	s.applyRemoteSink(sinkCfg(true))
	if _, closed := oldSink.state(); !closed {
		t.Error("replaced sink was not closed")
	}
	if _, closed := newSink.state(); closed {
		t.Error("active sink was closed")
	}
}

// If the new sink fails to initialise, the old one is still detached and closed.
func TestApplyRemoteSink_InitFailureDetachesOld(t *testing.T) {
	sink := &closableSink{}
	s := newSinkServer(t, sink, nil)
	s.applyRemoteSink(sinkCfg(true))
	s.applyRemoteSink(sinkCfg(true))
	n, closed := sink.state()
	s.logger.Info("after_failed_reload", nil)
	if after, _ := sink.state(); after != n {
		t.Error("old sink still receives logs after a failed reload")
	}
	if !closed {
		t.Error("old sink was not closed after a failed reload")
	}
}
