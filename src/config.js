import { readFile } from 'node:fs/promises';
import { existsSync } from 'node:fs';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = fileURLToPath(new URL('.', import.meta.url));

const defaults = {
  version: '1.0.0',
  dryRun: false,
  safeMode: true,
  shell: {
    allowedCommands: ['ls', 'pwd', 'cat', 'echo', 'mkdir', 'touch', 'cp', 'mv', 'rm', 'find', 'grep', 'termux-*'],
    blockedPatterns: ['rm -rf /', '> /dev/null', '| sh', 'curl .*| sh', 'wget .*| sh'],
    maxTimeoutMs: 30000
  }
};

export async function loadConfig(configPath) {
  const paths = [
    configPath,
    process.env.TERMUX_AI_CONFIG,
    '/data/data/com.termux/files/home/.termux-ai-agent.json',
    '/root/.termux-ai-agent.json',
    resolve(__dirname, '../config/default.json')
  ].filter(Boolean);

  let userConfig = {};
  for (const p of paths) {
    if (existsSync(p)) {
      try {
        const raw = await readFile(p, 'utf8');
        userConfig = JSON.parse(raw);
        break;
      } catch {
        // continue to next candidate
      }
    }
  }

  return mergeConfig(defaults, userConfig);
}

function mergeConfig(base, override) {
  const result = structuredClone(base);
  for (const key of Object.keys(override)) {
    if (override[key] && typeof override[key] === 'object' && !Array.isArray(override[key])) {
      result[key] = { ...result[key], ...override[key] };
    } else {
      result[key] = override[key];
    }
  }
  return result;
}
