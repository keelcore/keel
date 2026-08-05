package main

import (
	"os"

	"github.com/keelcore/keel/pkg/apps/keel"
)

func main() { os.Exit(keel.Run(nil)) }
