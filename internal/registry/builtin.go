package registry

import "strings"

const (
	DefaultReadCapBytes = 16384
	GroqReadCapBytes    = 3072
)

// legacyEnvKeyVars maps canonical builtin provider names to their legacy
// environment variable names.
var legacyEnvKeyVars = map[string]string{
	"anthropic":  "ANTHROPIC_API_KEY",
	"groq":       "GROQ_API_KEY",
	"openrouter": "OPENROUTER_API_KEY",
	"nvidia":     "NVIDIA_API_KEY",
}

// EnvKeyName returns the credential environment variable for a provider.
// Builtins preserve their historical names; custom providers derive one from
// their identifier. This is the sole derivation used by config and registry.
func EnvKeyName(providerID string) string {
	if v := legacyEnvKeyVars[providerID]; v != "" {
		return v
	}
	return strings.ToUpper(strings.ReplaceAll(providerID, "-", "_")) + "_API_KEY"
}

// LegacyEnvKey is kept for compatibility with callers that need to distinguish
// historical builtin variables from derived custom-provider variables.
func LegacyEnvKey(providerID string) string { return legacyEnvKeyVars[providerID] }

// BuiltinCatalog returns the immutable base catalog containing the four
// canonical providers: anthropic, groq, openrouter, and nvidia.
func BuiltinCatalog() map[string]ProviderConfig {
	return map[string]ProviderConfig{
		"anthropic": {
			API:  "anthropic",
			Name: "Anthropic",
			Options: ProviderOptions{
				BaseURL: "https://api.anthropic.com/v1",
			},
			Models: map[string]ModelConfig{
				"claude-sonnet-5": {
					Name: "Claude Sonnet 5",
					ID:   "claude-sonnet-5",
				},
				"claude-3.5-sonnet": {
					Name: "Claude 3.5 Sonnet",
					ID:   "claude-3-5-sonnet-20241022",
				},
				"claude-3.5-haiku": {
					Name: "Claude 3.5 Haiku",
					ID:   "claude-3-5-haiku-20241022",
				},
			},
			DefaultModel: "claude-sonnet-5",
			ReadCap:      DefaultReadCapBytes,
		},
		"groq": {
			API:  "openai",
			Name: "Groq",
			Options: ProviderOptions{
				BaseURL: "https://api.groq.com/openai/v1",
			},
			Models: map[string]ModelConfig{
				"openai/gpt-oss-120b": {
					Name: "OpenAI GPT OSS 120B",
					ID:   "openai/gpt-oss-120b",
				},
			},
			DefaultModel: "openai/gpt-oss-120b",
			ReadCap:      GroqReadCapBytes,
		},
		"openrouter": {
			API:  "openai",
			Name: "OpenRouter",
			Options: ProviderOptions{
				BaseURL: "https://openrouter.ai/api/v1",
			},
			Models: map[string]ModelConfig{
				"anthropic/claude-3.5-haiku": {
					Name: "Claude 3.5 Haiku",
					ID:   "anthropic/claude-3.5-haiku",
				},
			},
			DefaultModel: "anthropic/claude-3.5-haiku",
			ReadCap:      DefaultReadCapBytes,
		},
		"nvidia": {
			API:  "openai",
			Name: "NVIDIA NIM",
			Options: ProviderOptions{
				BaseURL: "https://integrate.api.nvidia.com/v1",
			},
			Models: map[string]ModelConfig{
				"moonshotai/kimi-k2.6": {
					Name: "Moonshot Kimi k2.6",
					ID:   "moonshotai/kimi-k2.6",
				},
			},
			DefaultModel: "moonshotai/kimi-k2.6",
			ReadCap:      DefaultReadCapBytes,
		},
	}
}
