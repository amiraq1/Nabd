import { describe, it, before } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { executeFile } from '../src/executors/file.js';

describe('executeFile', () => {
  let tmpDir;

  before(async () => {
    tmpDir = await mkdtemp(join(tmpdir(), 'termux-ai-'));
  });

  it('reads an existing file', async () => {
    const target = join(tmpDir, 'read.txt');
    await writeFile(target, 'hello', 'utf8');
    const result = await executeFile({ action: 'read', payload: target }, { dryRun: false });
    assert.equal(result.content, 'hello');
  });

  it('creates a new file', async () => {
    const target = join(tmpDir, 'create.txt');
    const result = await executeFile({ action: 'create', payload: `${target} world` }, { dryRun: false });
    assert.equal(result.bytesWritten, 5);
  });

  it('appends to a file', async () => {
    const target = join(tmpDir, 'append.txt');
    await writeFile(target, 'hello', 'utf8');
    const result = await executeFile({ action: 'append', payload: `${target} world` }, { dryRun: false });
    assert.equal(result.bytesAppended, 5);
  });
});
