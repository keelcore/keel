package main

import (
	"os"

	"github.com/keelcore/keel/pkg/apps/configschema"
)

func main() { os.Exit(configschema.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }
