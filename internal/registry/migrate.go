package registry

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MigrationSummary contains the result of migrating legacy configuration.
type MigrationSummary struct {
	ConfigFound      bool
	EnvFound         bool
	Providers        []string
	AuthFile         string
	ProvidersFile    string
	BackupConfigFile string
	BackupEnvFile    string
}

func (s MigrationSummary) String() string {
	var b strings.Builder
	if !s.ConfigFound && !s.EnvFound {
		return "migration: no legacy ~/.ag/config or ~/.ag/env found; nothing to migrate"
	}
	b.WriteString("Migration summary:\n")
	if len(s.Providers) > 0 {
		b.WriteString(fmt.Sprintf("  - Migrated providers (%d): %s\n", len(s.Providers), strings.Join(s.Providers, ", ")))
	} else {
		b.WriteString("  - No provider credentials found to migrate\n")
	}
	if s.AuthFile != "" {
		b.WriteString(fmt.Sprintf("  - Written credentials: %s (mode 0600)\n", s.AuthFile))
	}
	if s.ProvidersFile != "" {
		b.WriteString(fmt.Sprintf("  - Written catalog: %s (mode 0600)\n", s.ProvidersFile))
	}
	if s.BackupConfigFile != "" {
		b.WriteString(fmt.Sprintf("  - Preserved backup: %s (mode 0600)\n", s.BackupConfigFile))
	}
	if s.BackupEnvFile != "" {
		b.WriteString(fmt.Sprintf("  - Preserved backup: %s (mode 0600)\n", s.BackupEnvFile))
	}
	return strings.TrimRight(b.String(), "\n")
}

// Migrate reads config and env files from agDir, writes providers.json and auth.json (mode 0600),
// preserves backups (config.bak, env.bak) without deleting originals, and returns a summary.
func Migrate(agDir string) (MigrationSummary, error) {
	if agDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return MigrationSummary{}, err
		}
		agDir = filepath.Join(home, ".ag")
	}

	configPath := filepath.Join(agDir, "config")
	envPath := filepath.Join(agDir, "env")

	configValues := make(map[string]string)
	var configFound, envFound bool

	// Read config file if it exists
	if cBytes, err := os.ReadFile(configPath); err == nil {
		configFound = true
		if vals, pErr := parseSimpleKV(cBytes); pErr == nil {
			for k, v := range vals {
				configValues[k] = v
			}
		}
	}

	// Read env file if it exists
	if eBytes, err := os.ReadFile(envPath); err == nil {
		envFound = true
		if vals, pErr := parseSimpleKV(eBytes); pErr == nil {
			for k, v := range vals {
				if _, ok := configValues[k]; !ok {
					configValues[k] = v
				}
			}
		}
	}

	if !configFound && !envFound {
		return MigrationSummary{}, nil
	}

	authData := make(AuthFile)
	provData := BuiltinCatalog()

	// Map legacy keys to auth.json entries
	legacyMapping := map[string]string{
		"ANTHROPIC_API_KEY":  "anthropic",
		"GROQ_API_KEY":       "groq",
		"OPENROUTER_API_KEY": "openrouter",
		"NVIDIA_API_KEY":     "nvidia",
	}

	for varName, providerID := range legacyMapping {
		if val, ok := configValues[varName]; ok && strings.TrimSpace(val) != "" {
			authData[providerID] = AuthConfig{
				Type: "api",
				Key:  strings.TrimSpace(val),
			}
		}
	}

	// Also look for any other *_API_KEY
	for k, v := range configValues {
		if strings.HasSuffix(k, "_API_KEY") && strings.TrimSpace(v) != "" {
			prefix := strings.ToLower(strings.TrimSuffix(k, "_API_KEY"))
			if _, exists := authData[prefix]; !exists && validateProviderID(prefix) == nil {
				authData[prefix] = AuthConfig{
					Type: "api",
					Key:  strings.TrimSpace(v),
				}
			}
		}
	}

	// If NABD_PROVIDER and NABD_BASE_URL or NABD_MODEL exist, record them
	activeProvider := strings.TrimSpace(configValues["NABD_PROVIDER"])
	customBaseURL := strings.TrimSpace(configValues["NABD_BASE_URL"])
	customModel := strings.TrimSpace(configValues["NABD_MODEL"])

	if activeProvider != "" && activeProvider != "router" {
		pCfg, exists := provData[activeProvider]
		if !exists {
			pCfg = ProviderConfig{
				API:     "openai",
				Name:    activeProvider,
				Models:  make(map[string]ModelConfig),
				ReadCap: DefaultReadCapBytes,
			}
		}
		if customBaseURL != "" {
			pCfg.Options.BaseURL = customBaseURL
		}
		if customModel != "" {
			if pCfg.Models == nil {
				pCfg.Models = make(map[string]ModelConfig)
			}
			pCfg.Models[customModel] = ModelConfig{
				Name: customModel,
				ID:   customModel,
			}
		}
		provData[activeProvider] = pCfg
	}

	// Write auth.json
	authPath := filepath.Join(agDir, "auth.json")
	if err := WriteAuthFile(authPath, authData); err != nil {
		return MigrationSummary{}, fmt.Errorf("write auth.json: %w", err)
	}

	// Write providers.json
	providersPath := filepath.Join(agDir, "providers.json")
	if err := WriteProvidersFile(providersPath, provData); err != nil {
		return MigrationSummary{}, fmt.Errorf("write providers.json: %w", err)
	}

	summary := MigrationSummary{
		ConfigFound:   configFound,
		EnvFound:      envFound,
		AuthFile:      authPath,
		ProvidersFile: providersPath,
	}

	// Backup config -> config.bak without deleting original
	if configFound {
		bakPath := filepath.Join(agDir, "config.bak")
		cBytes, err := os.ReadFile(configPath)
		if err != nil {
			return MigrationSummary{}, fmt.Errorf("read config for backup: %w", err)
		}
		if err := writeSecureFile(bakPath, cBytes); err != nil {
			return MigrationSummary{}, fmt.Errorf("write config.bak: %w", err)
		}
		summary.BackupConfigFile = bakPath
	}

	// Backup env -> env.bak without deleting original
	if envFound {
		bakPath := filepath.Join(agDir, "env.bak")
		eBytes, err := os.ReadFile(envPath)
		if err != nil {
			return MigrationSummary{}, fmt.Errorf("read env for backup: %w", err)
		}
		if err := writeSecureFile(bakPath, eBytes); err != nil {
			return MigrationSummary{}, fmt.Errorf("write env.bak: %w", err)
		}
		summary.BackupEnvFile = bakPath
	}

	for id := range authData {
		summary.Providers = append(summary.Providers, id)
	}
	sort.Strings(summary.Providers)

	// Ensure no keys leak anywhere in the summary output
	summaryStr := summary.String()
	for _, a := range authData {
		if a.Key != "" && strings.Contains(summaryStr, a.Key) {
			return MigrationSummary{}, fmt.Errorf("migration error: summary contained raw credentials")
		}
	}

	return summary, nil
}

func writeSecureFile(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func parseSimpleKV(data []byte) (map[string]string, error) {
	out := make(map[string]string)
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		if k != "" {
			out[k] = v
		}
	}
	return out, sc.Err()
}
