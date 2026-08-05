// examples/myapp/main.go
// Demonstrates embedding keel as a library.
// Configuration: set APP_CONFIG and APP_SECRETS to YAML file paths.
package main

import (
	"os"

	"github.com/keelcore/keel/pkg/apps/myapp"
)

func main() { os.Exit(myapp.Run()) }
