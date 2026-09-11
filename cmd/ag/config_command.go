package main

import (
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"nabd/internal/config"
)

func runConfigCommand(args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(errOut, "usage: nabd config path|validate|show --redacted")
		return 2
	}
	path, version, err := config.SelectedPath()
	if err != nil {
		fmt.Fprintln(errOut, "nabd config:", err)
		return 1
	}
	switch args[0] {
	case "path":
		if len(args) != 1 {
			fmt.Fprintln(errOut, "usage: nabd config path")
			return 2
		}
		fmt.Fprintf(out, "%s (v%d)\n", path, version)
		return 0
	case "validate":
		if len(args) != 1 {
			fmt.Fprintln(errOut, "usage: nabd config validate")
			return 2
		}
		vals, selectedVersion, err := config.ParseSelectedFile()
		if err != nil {
			fmt.Fprintln(errOut, "invalid config:", err)
			return 1
		}
		if selectedVersion == 1 {
			printConfigWarnings(errOut, vals)
		}
		fmt.Fprintf(out, "config valid (v%d)\n", selectedVersion)
		return 0
	case "show":
		fs := flag.NewFlagSet("config show", flag.ContinueOnError)
		fs.SetOutput(errOut)
		redacted := fs.Bool("redacted", false, "redact credential values")
		if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 || !*redacted {
			fmt.Fprintln(errOut, "usage: nabd config show --redacted")
			return 2
		}
		vals, selectedVersion, err := config.ParseSelectedFile()
		if err != nil {
			fmt.Fprintln(errOut, "invalid config:", err)
			return 1
		}
		if selectedVersion == 1 {
			printConfigWarnings(errOut, vals)
		}
		keys := make([]string, 0, len(vals))
		for k := range vals { keys = append(keys, k) }
		sort.Strings(keys)
		for _, k := range keys {
			v := vals[k]
			if sensitiveConfigKey(k) && v != "" { v = "<redacted>" }
			fmt.Fprintf(out, "%s=%s\n", k, v)
		}
		return 0
	default:
		fmt.Fprintf(errOut, "unknown config command %q\n", args[0])
		return 2
	}
}

func printConfigWarnings(w io.Writer, vals map[string]string) {
	for _, warning := range config.Warnings(vals) { fmt.Fprintln(w, "warning:", warning) }
}

func sensitiveConfigKey(key string) bool {
	upper := strings.ToUpper(key)
	for _, marker := range []string{"KEY", "TOKEN", "SECRET", "PASSWORD", "AUTH", "CREDENTIAL"} {
		if strings.Contains(upper, marker) { return true }
	}
	return false
}
