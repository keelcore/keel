//go:build !windows

package logging

// renameBusyOS: only Windows refuses a rename because another process has the file open.
func renameBusyOS(error) bool { return false }
