package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

// init wires the custom help before flag.Parse runs in main. /undo and /rewind
// are typed in the interactive prompt, not passed as flags, so without this
// section `nabd --help` never mentions the two commands a user needs to
// recover from an agent edit — which is how the model ended up suggesting
// git checkout instead.
func init() {
	flag.Usage = func() { printUsage(flag.CommandLine.Output()) }
}

// printUsage writes the flag list plus the interactive slash commands. The
// command lines mirror the README table verbatim: help that disagrees with the
// documented contract is worse than no help. Only the recovery commands are
// listed here; /help in a session lists the rest.
func printUsage(w io.Writer) {
	fmt.Fprintf(w, "Usage of %s:\n", os.Args[0])
	old := flag.CommandLine.Output()
	flag.CommandLine.SetOutput(w)
	flag.PrintDefaults()
	flag.CommandLine.SetOutput(old)
	fmt.Fprint(w, "\nSlash commands (typed in an interactive session):\n")
	fmt.Fprintf(w, "  %-14s %s\n", "/undo [n]", "undo file edits recorded in the journal")
	fmt.Fprintf(w, "  %-14s %s\n", "/rewind [n]", "rewind conversation turns and restore prompt")
	fmt.Fprint(w, "  /help in a session lists every command.\n")
}
