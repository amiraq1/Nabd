package main

import (
	"os"

	"nabd/internal/sandbox"
)

// The Bash child uses the same binary as a short-lived helper so Landlock can
// be installed before the process is replaced by the requested shell.
func init() {
	if len(os.Args) > 1 && os.Args[1] == sandbox.HelperCommand {
		os.Exit(sandbox.Run(os.Args[2:]))
	}
}
