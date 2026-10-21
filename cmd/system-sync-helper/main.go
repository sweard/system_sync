// system-sync-helper contains the portable parts of the repository's tasks.
package main

import (
	"os"

	"github.com/sweard/system_sync/internal/helpercli"
)

// Set by the explicit build; an unversioned go build is never installed by tasks.
var version = "dev"
var revision = "unknown"

func main() {
	os.Exit(helpercli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, version, revision))
}
