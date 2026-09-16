// Package providercmd holds the logic behind the `nabd connect`, `nabd models`,
// and `nabd provider` commands. It is separated from cmd/ag so the security
// contract — a key only ever arrives through a hidden prompt, and a missing key
// is reported with the file it belongs in — is tested without a terminal.
package providercmd

import (
	"errors"
	"fmt"
	"strings"

	"nabd/internal/registry"
)

// ErrKeyAsArgument is returned when a key-looking token, or any second
// argument, reaches `nabd connect`. The key is read from a hidden prompt and
// is never accepted on the command line.
var ErrKeyAsArgument = errors.New(
	"refusing a key on the command line — run `nabd connect <provider>` and type the key at the hidden prompt")

// keyPrefixes are the recognizable credential prefixes. A provider identifier
// is at most 32 bytes of [a-z0-9-_], but several of those strings are also
// valid identifiers, so the prefix check runs first.
var keyPrefixes = []string{"sk-", "sk_", "gsk_", "nvapi-", "xai-", "AIza", "Bearer "}

// ParseConnectArgs validates the arguments of `nabd connect`. Exactly one
// provider identifier is accepted; a key-looking token or any extra argument is
// refused, so the secret can only arrive through the hidden prompt.
func ParseConnectArgs(args []string) (string, error) {
	switch len(args) {
	case 0:
		return "", errors.New("usage: nabd connect <provider>")
	case 1:
		raw := strings.TrimSpace(args[0])
		if looksLikeKey(raw) {
			return "", ErrKeyAsArgument
		}
		id := strings.ToLower(raw)
		if err := registry.ValidProviderID(id); err != nil {
			return "", err
		}
		return id, nil
	default:
		return "", ErrKeyAsArgument
	}
}

func looksLikeKey(s string) bool {
	for _, p := range keyPrefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// Connect reads a key through readKey — which must not echo it — and writes it
// to authPath at mode 0600. It returns a redacted summary and never includes
// the key in that summary or in any error.
func Connect(authPath, providerID string, readKey func() (string, error)) (string, error) {
	if err := registry.ValidProviderID(providerID); err != nil {
		return "", err
	}
	if readKey == nil {
		return "", errors.New("no key reader available")
	}
	key, err := readKey()
	if err != nil {
		return "", err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", errors.New("empty key; nothing written")
	}

	af, err := registry.ParseAuthFile(authPath)
	if err != nil {
		return "", err
	}
	if af == nil {
		af = registry.AuthFile{}
	}
	af[providerID] = registry.AuthConfig{Type: "api", Key: key}
	if err := registry.WriteAuthFile(authPath, af); err != nil {
		return "", err
	}
	return fmt.Sprintf("stored api credential for %q in %s (mode 0600)", providerID, authPath), nil
}
