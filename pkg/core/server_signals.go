//go:build !windows

package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// runSignalLoop handles SIGHUP (reload), SIGUSR1 (dump config), and SIGUSR2
// (log rotation placeholder) until ctx is cancelled.
func (s *Server) runSignalLoop(ctx context.Context) {
	ch := make(chan os.Signal, 4)
	signal.Notify(ch, syscall.SIGHUP, syscall.SIGUSR1, syscall.SIGUSR2)
	defer signal.Stop(ch)

	for {
		select {
		case sig := <-ch:
			switch sig {
			case syscall.SIGHUP:
				if err := s.Reload(); err != nil {
					s.logger.Warn("sighup_reload_failed", map[string]any{"err": err.Error()})
				}
			case syscall.SIGUSR1:
				s.dumpConfig()
			case syscall.SIGUSR2:
				s.logger.Info("sigusr2_received", map[string]any{"note": "log rotation not yet configured"})
			}
		case <-ctx.Done():
			return
		}
	}
}

// dumpConfig writes the current configuration as JSON to stderr (SIGUSR1).
func (s *Server) dumpConfig() {
	s.cfgMu.RLock()
	cfg := s.cfg
	s.cfgMu.RUnlock()
	b, _ := json.MarshalIndent(cfg, "", "  ")
	fmt.Fprintln(os.Stderr, string(b))
}
