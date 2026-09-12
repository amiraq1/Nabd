package main

import "os"

// Config diagnostics must run before the legacy flag parser sees the
// subcommand. Keeping the dispatch isolated also leaves main's interactive
// startup path unchanged.
func init() {
	if len(os.Args) > 1 && os.Args[1] == "config" {
		os.Exit(runConfigCommand(os.Args[2:], os.Stdout, os.Stderr))
	}
}
