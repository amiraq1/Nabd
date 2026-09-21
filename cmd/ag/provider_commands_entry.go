package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"

	"nabd/internal/endpoint"
)

// Provider commands must run before the legacy flag parser sees the
// subcommand, matching the migrate and config dispatch.
func init() {
	if len(os.Args) < 2 {
		return
	}
	switch os.Args[1] {
	case "connect":
		os.Exit(runConnectCommand(os.Args[2:], os.Stdout, os.Stderr, termReadKey))
	case "models":
		os.Exit(runModelsCommand(os.Args[2:], os.Stdout, os.Stderr, endpoint.Client(0)))
	case "provider":
		os.Exit(runProviderCommand(os.Args[2:], os.Stdout, os.Stderr))
	}
}

// termReadKey prompts on stderr and reads the key with echo disabled. When
// stdin is not a terminal ReadPassword fails, so the key can never be piped in
// by accident either.
func termReadKey() (string, error) {
	fmt.Fprint(os.Stderr, "API key (input hidden): ")
	b, err := term.ReadPassword(os.Stdin.Fd())
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
