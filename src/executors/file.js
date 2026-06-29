import { readFile, writeFile, appendFile, unlink, access, mkdir } from 'node:fs/promises';
import { existsSync } from 'node:fs';

export async function executeFile(step, config) {
  const { action, payload } = step;

  if (!payload || typeof payload !== 'string') {
    throw new Error('File executor requires a payload string');
  }

  switch (action) {
    case 'read':
      return read(payload, config);
    case 'create':
      return create(payload, config);
    case 'write':
      return write(payload, config);
    case 'append':
      return append(payload, config);
    case 'delete':
      return remove(payload, config);
    default:
      throw new Error(`Unsupported file action: ${action}`);
  }
}

function parsePayload(payload) {
  const parts = payload.split(/\s+(?=(?:[^"]*"[^"]*")*[^"]*$)/);
  if (parts.length < 1) throw new Error('Missing file path');
  return {
    path: parts[0].replace(/^"|"$/g, ''),
    content: parts.slice(1).join(' ').replace(/^"|"$/g, '')
  };
}

async function read(payload, config) {
  const { path } = parsePayload(payload);
  if (config.dryRun) return `[dry-run] would read: ${path}`;
  const content = await readFile(path, 'utf8');
  return { action: 'read', path, content };
}

async function create(payload, config) {
  const { path, content } = parsePayload(payload);
  if (existsSync(path)) throw new Error(`File already exists: ${path}`);
  if (config.dryRun) return `[dry-run] would create: ${path}`;
  await writeFile(path, content || '', 'utf8');
  return { action: 'create', path, bytesWritten: Buffer.byteLength(content || '') };
}

async function write(payload, config) {
  const { path, content } = parsePayload(payload);
  if (config.dryRun) return `[dry-run] would write: ${path}`;
  await writeFile(path, content || '', 'utf8');
  return { action: 'write', path, bytesWritten: Buffer.byteLength(content || '') };
}

async function append(payload, config) {
  const { path, content } = parsePayload(payload);
  if (config.dryRun) return `[dry-run] would append to: ${path}`;
  await appendFile(path, content || '', 'utf8');
  return { action: 'append', path, bytesAppended: Buffer.byteLength(content || '') };
}

async function remove(payload, config) {
  const { path } = parsePayload(payload);
  if (config.dryRun) return `[dry-run] would delete: ${path}`;
  await unlink(path);
  return { action: 'delete', path };
}
