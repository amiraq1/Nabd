import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { executeShell } from '../src/executors/shell.js';

describe('executeShell', () => {
  it('executes an allowed command', async () => {
    const config = {
      dryRun: false,
      safeMode: true,
      shell: {
        allowedCommands: ['echo'],
        blockedPatterns: [],
        maxTimeoutMs: 5000
      }
    };
    const result = await executeShell({ command: 'echo hello' }, config);
    assert.equal(result.exitCode, 0);
    assert.equal(result.stdout, 'hello');
  });

  it('returns dry-run output without executing', async () => {
    const config = {
      dryRun: true,
      safeMode: true,
      shell: {
        allowedCommands: ['echo'],
        blockedPatterns: [],
        maxTimeoutMs: 5000
      }
    };
    const result = await executeShell({ command: 'echo hello' }, config);
    assert.equal(result, '[dry-run] would execute: echo hello');
  });

  it('blocks disallowed commands', async () => {
    const config = {
      dryRun: false,
      safeMode: true,
      shell: {
        allowedCommands: ['echo'],
        blockedPatterns: [],
        maxTimeoutMs: 5000
      }
    };
    await assert.rejects(
      () => executeShell({ command: 'rm -rf /tmp' }, config),
      /Command not allowed: rm/
    );
  });
});
