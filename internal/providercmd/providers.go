package providercmd

import (
	"fmt"
	"strings"

	"nabd/internal/registry"
)

// ProviderStatus is one row of `nabd provider`.
type ProviderStatus struct {
	ID        string
	Name      string
	Dialect   string
	Source    string // providers.json | builtin
	KeySource string // auth.json | env | none
	Models    int
}

// DescribeProviders lists every configured provider with where its definition
// and its key came from.
func DescribeProviders(reg *registry.Registry) []ProviderStatus {
	provs := reg.Providers()
	out := make([]ProviderStatus, 0, len(provs))
	for _, p := range provs {
		name := p.Name
		if name == "" {
			name = p.ID
		}
		out = append(out, ProviderStatus{
			ID:        p.ID,
			Name:      name,
			Dialect:   p.API,
			Source:    p.Source,
			KeySource: p.KeySource,
			Models:    len(p.Models),
		})
	}
	return out
}

// FormatProviders renders one provider per line. A missing key is stated with
// the file it belongs in. No key value is ever printed.
func FormatProviders(list []ProviderStatus, reg *registry.Registry) string {
	var b strings.Builder
	for _, p := range list {
		key := "key:" + p.KeySource
		if p.KeySource == "" || p.KeySource == "none" {
			key = "MISSING key — add it to " + reg.AuthPath
		}
		n := "models"
		if p.Models == 1 {
			n = "model"
		}
		fmt.Fprintf(&b, "%-20s %-9s def:%-14s %d %s  %s\n",
			p.ID, p.Dialect, p.Source, p.Models, n, key)
	}
	return strings.TrimRight(b.String(), "\n")
}
