# Ideas and Future Work

- **Secure API Key Storage**: Extract the provider API keys (`NVIDIA_API_KEY`, `ANTHROPIC_API_KEY`, etc.) from the environment variables into a separate config file located outside the project root (e.g., `~/.ag/config`). This ensures that `scrubEnv` doesn't have to perfectly filter the environment, and a rogue command like `cat ~/.bashrc` cannot accidentally leak credentials.
