//go:build windows

package logging

import (
	"errors"
	"syscall"
)

// ERROR_ACCESS_DENIED and ERROR_SHARING_VIOLATION: another process, such as a virus scanner, has
// the file open for a moment.
const (
	errorAccessDenied     = syscall.Errno(5)
	errorSharingViolation = syscall.Errno(32)
)

func renameBusyOS(err error) bool {
	return errors.Is(err, errorSharingViolation) || errors.Is(err, errorAccessDenied)
}
