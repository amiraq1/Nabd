import { exec } from 'node:child_process';
import { existsSync } from 'node:fs';

export function isTermux() {
  return (
    process.env.TERMUX_VERSION !== undefined ||
    process.env.PREFIX === '/data/data/com.termux/files/usr' ||
    existsSync('/data/data/com.termux/files/usr/bin/termux-api') ||
    process.cwd().startsWith('/data/data/com.termux/files')
  );
}

export function getTermuxPrefix() {
  if (process.env.PREFIX) return process.env.PREFIX;
  if (isTermux()) return '/data/data/com.termux/files/usr';
  return '/usr';
}

export function termuxCommand(name, args = []) {
  const prefix = getTermuxPrefix();
  const binary = `${prefix}/bin/${name}`;
  return [binary, ...args].join(' ');
}

export async function checkTermuxApi() {
  return new Promise((resolve) => {
    exec('termux-api-start --help', (error) => {
      resolve(!error);
    });
  });
}

export function getDeviceInfo() {
  return {
    isTermux: isTermux(),
    prefix: getTermuxPrefix(),
    cwd: process.cwd(),
    version: process.env.TERMUX_VERSION || 'not detected'
  };
}
