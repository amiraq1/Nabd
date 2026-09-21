package main

import (
	"os"
)

func init() {
	if len(os.Args) > 1 && os.Args[1] == "purge" {
		os.Exit(runPurgeCommand(os.Args[2:], os.Stdout, os.Stderr))
	}
}
