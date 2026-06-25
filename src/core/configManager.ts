import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';

export const CONFIG_DIR = path.join(os.homedir(), '.config', 'nabd');
export const CONFIG_FILE = path.join(CONFIG_DIR, 'config.json');

export interface NabdConfig {
  model: string;
  endpoint: string;
  vectorDbPath: string;
  maxContextTokens: number;
}

const DEFAULT_CONFIG: NabdConfig = {
  model: 'llama3', // نموذجك المفضل محلياً
  endpoint: 'http://127.0.0.1:11434',
  vectorDbPath: path.join(CONFIG_DIR, 'semantic_memory.sqlite'),
  maxContextTokens: 4096,
};

export const loadConfig = (): NabdConfig => {
  if (!fs.existsSync(CONFIG_DIR)) {
    fs.mkdirSync(CONFIG_DIR, { recursive: true });
  }

  if (!fs.existsSync(CONFIG_FILE)) {
    fs.writeFileSync(CONFIG_FILE, JSON.stringify(DEFAULT_CONFIG, null, 2), 'utf-8');
    return DEFAULT_CONFIG;
  }

  try {
    const raw = fs.readFileSync(CONFIG_FILE, 'utf-8');
    return { ...DEFAULT_CONFIG, ...JSON.parse(raw) };
  } catch (error) {
    return DEFAULT_CONFIG; // العودة للوضع الآمن في حال تلف التنسيق
  }
};
