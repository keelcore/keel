//go:build windows

package core

import "context"

// runSignalLoop is a no-op on Windows: SIGHUP/SIGUSR1/SIGUSR2 are not
// delivered by the OS. Use POST /admin/reload for config reloads.
func (s *Server) runSignalLoop(_ context.Context) {}