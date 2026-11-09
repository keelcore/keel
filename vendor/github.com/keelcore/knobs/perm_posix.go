//go:build !windows

package knobs

import (
	"fmt"
	"io/fs"
)

// ownerOnly reports, as an error, a secrets file its owner alone cannot claim: on systems whose file modes carry
// access rights, any group or other bit set.
func ownerOnly(path string, info fs.FileInfo) error {
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Errorf("%s has mode %04o; the owner alone may have access", path, perm)
	}
	return nil
}
