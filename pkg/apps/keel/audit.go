package keel

import (
	"fmt"
	"io"

	"github.com/keelcore/keel/pkg/core/audit"
)

// runAudit is `keel audit verify FILE`: it checks the hash chain across FILE and its rotated
// backups (FILE.<time>[.gz]), oldest first. Exit 0 when intact, 1 when not, 2 on bad usage.
func runAudit(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 || args[0] != "verify" {
		fmt.Fprintln(stderr, "usage: keel audit verify FILE")
		return 2
	}
	files, err := audit.Files(args[1])
	if err == nil {
		var n int
		if n, err = audit.VerifyFiles(files); err == nil {
			fmt.Fprintf(stdout, "ok: %d entries in %d files\n", n, len(files))
			return 0
		}
	}
	fmt.Fprintf(stderr, "audit verify failed: %v\n", err)
	return 1
}
