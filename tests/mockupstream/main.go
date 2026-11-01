// tests/mockupstream/main.go
// A deliberately badly behaved upstream for keel's sidecar BATS, built on the
// pinned keel release as an unmodified library. See package mock.
package main

import (
	"os"

	"github.com/keelcore/keel/tests/mockupstream/mock"
)

func main() { os.Exit(mock.Run()) }
