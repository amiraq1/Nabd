import { readFileSync, existsSync } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';

export interface NabdConfig {
  provider: string;
  model: string;
  endpoint: string;
  nvidiaApiKey?: string;
  openaiApiKey?: string;
}

export function loadNabdConfig(): NabdConfig {
  const configPath = join(homedir(), '.config', 'nabd', 'config.json');
  if (existsSync(configPath)) {
    try {
      const raw = readFileSync(configPath, 'utf-8');
      return JSON.parse(raw);
    } catch (e) {
      console.error('Failed to parse config:', e);
    }
  }
  
  return {
    provider: 'ollama',
    model: process.env.OLLAMA_MODEL || 'llama3',
    endpoint: process.env.OLLAMA_URL || 'http://localhost:11434'
  };
}

export const globalConfig = loadNabdConfig();
