package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"nabd/internal/endpoint"
	"nabd/internal/providercmd"
	"nabd/internal/registry"
)

// runConnectCommand implements `nabd connect <provider>`. The key is read
// through readKey — a hidden prompt in production — and written to auth.json at
// mode 0600. It is never accepted as an argument.
func runConnectCommand(args []string, out, errOut io.Writer, readKey func() (string, error)) int {
	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	fs.SetOutput(errOut)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	id, err := providercmd.ParseConnectArgs(fs.Args())
	if err != nil {
		fmt.Fprintln(errOut, "nabd connect:", err)
		return 2
	}
	_, authPath, err := registry.DefaultPaths()
	if err != nil {
		fmt.Fprintln(errOut, "nabd connect:", err)
		return 1
	}
	summary, err := providercmd.Connect(authPath, id, readKey)
	if err != nil {
		fmt.Fprintln(errOut, "nabd connect:", err)
		return 1
	}
	fmt.Fprintln(out, summary)
	return 0
}

// runModelsCommand implements `nabd models <provider>`: it asks the endpoint
// itself for the models it serves, so the answer is the live catalog and not a
// copy that rots. A failure is printed with the existing provider error code.
func runModelsCommand(args []string, out, errOut io.Writer, client *http.Client) int {
	fs := flag.NewFlagSet("models", flag.ContinueOnError)
	fs.SetOutput(errOut)
	timeout := fs.Duration("timeout", 20*time.Second, "network timeout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(errOut, "usage: nabd models <provider> [--timeout 20s]")
		return 2
	}
	id := strings.ToLower(strings.TrimSpace(fs.Arg(0)))

	reg, err := registry.Load()
	if err != nil {
		fmt.Fprintln(errOut, "nabd models:", err)
		return 1
	}
	prov, ok := reg.Get(id)
	if !ok {
		fmt.Fprintf(errOut, "nabd models: provider %q is not configured — see `nabd provider` and add it to %s\n",
			id, reg.ProvidersPath)
		return 1
	}
	if prov.Key == "" {
		fmt.Fprintf(errOut, "nabd models: provider %q has no API key — add it to %s with `nabd connect %s`\n",
			id, reg.AuthPath, id)
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if client == nil {
		client = endpoint.Client(*timeout)
	}
	ids, err := providercmd.FetchModels(ctx, prov.API, prov.BaseURL, prov.Key, client)
	if err != nil {
		fmt.Fprintf(errOut, "nabd models: provider_%s: %v\n", providercmd.KindOf(err), err)
		return 1
	}
	for _, m := range ids {
		fmt.Fprintln(out, m)
	}
	// The list is the endpoint's catalog, not a verdict on the credential.
	fmt.Fprintln(errOut, providercmd.CatalogIsNotACredentialCheck)
	return 0
}

// runProviderCommand implements `nabd provider`: the configured providers, who
// is missing a key, and which file each definition and key came from.
func runProviderCommand(args []string, out, errOut io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintln(errOut, "usage: nabd provider")
		return 2
	}
	reg, err := registry.Load()
	if err != nil {
		fmt.Fprintln(errOut, "nabd provider:", err)
		return 1
	}
	fmt.Fprintln(out, providercmd.FormatProviders(providercmd.DescribeProviders(reg), reg))
	return 0
}
