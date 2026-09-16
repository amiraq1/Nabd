package main

import (
	"flag"
	"fmt"
	"io"

	"nabd/internal/registry"
)

func runMigrateCommand(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.SetOutput(errOut)
	dir := fs.String("dir", "", "path to .ag directory (default ~/.ag)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(errOut, "usage: nabd migrate [--dir <path>]")
		return 2
	}
	summary, err := registry.Migrate(*dir)
	if err != nil {
		fmt.Fprintln(errOut, "nabd migrate:", err)
		return 1
	}
	fmt.Fprintln(out, summary.String())
	return 0
}
