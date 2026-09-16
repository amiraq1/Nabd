package main

import "os"

// Migration command runs before legacy flag parser to preserve standard CLI dispatch.
func init() {
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		os.Exit(runMigrateCommand(os.Args[2:], os.Stdout, os.Stderr))
	}
}
