import type {
  ToolContext,
  ToolDefinition,
  ExecutionPolicy,
  ToolExecutionResult,
  ToolStreamHandler,
} from '../types.js';
import { ProcessSession } from '../ProcessSession.js';
import { processManager } from '../process-manager.js';

/**
 * JSON Schema for execute_bash tool.
 * Describes the shape expected by the LLM/tool caller.
 */
export const executeBashSchema = {
  name: 'execute_bash',
  description:
    'Executes a bash command strictly within the Termux environment. Supports pipes and redirects. Use for system navigation, file inspection, and running tools.',
  parameters: {
    type: 'object',
    properties: {
      command: {
        type: 'string',
        description:
          'The exact bash command to execute (e.g., "ls -la" or "find . -type f | wc -l").',
      },
    },
    required: ['command'],
  },
};

/**
 * Default path to Termux bash.
 * Can be overridden via TERMUX_BASH_PATH environment variable for testing.
 */
const DEFAULT_BASH_PATH = '/data/data/com.termux/files/usr/bin/bash';

/**
 * Default execution policy for the bash tool. Mirrors the legacy hard-coded
 * limits previously embedded in `executeBash` so behaviour is unchanged.
 */
export function getBashPolicy(): Partial<ExecutionPolicy> {
  return {
    maxExecutionTimeMs: 45000,
    maxOutputBytes: 20 * 1024 * 1024,
    allowNetwork: true,
    allowFilesystemWrite: true,
    allowDelete: false,
    allowBackgroundProcess: false,
    workingDirectory: process.cwd(),
    environment: { ...process.env },
    // Empty means "no restriction" per PolicyEngine semantics; the bash
    // executable itself is the command being invoked, not its arguments.
    allowedCommands: [],
  };
}

/**
 * Backward-compatible helper that runs a bash command and returns the legacy
 * `ToolExecutionResult` shape. Internally uses the v2 runtime exactly once.
 */
export async function executeBash(
  command: string,
  onStream?: ToolStreamHandler,
  signal?: AbortSignal,
): Promise<ToolExecutionResult> {
  if (typeof command !== 'string' || command.trim().length === 0) {
    throw new Error('executeBash: command must be a non-empty string');
  }

  const bashPath = process.env.TERMUX_BASH_PATH ?? DEFAULT_BASH_PATH;
  const options = {
    command: bashPath,
    args: ['-c', command],
    cwd: process.cwd(),
    env: { ...process.env },
    signal,
  };

  const session = await processManager.run(options);

  if (onStream) {
    // Fire-and-forget stream consumer that forwards events to the callback.
    (async () => {
      for await (const event of session.stream()) {
        if (event.type === 'stdout') onStream(event.chunk, false);
        if (event.type === 'stderr') onStream(event.chunk, true);
      }
    })().catch(() => {
      // The stream consumer is best-effort; downstream consumers may be
      // attached later via session.stream() if needed.
    });
  }

  // Drain the stream so the session reaches a terminal state and the
  // snapshot is fully populated before we construct the legacy result.
  for await (const _event of session.stream()) {
    // no-op
  }

  const snap = session.snapshot();
  return {
    executionId: snap.executionId,
    exitCode: snap.exitCode,
    signal: snap.signal,
    durationMs: snap.durationMs ?? 0,
    timedOut: snap.timedOut,
    truncated: snap.truncated,
    outputByteCount: snap.totalBytes,
  };
}

/**
 * Full `ToolDefinition` for execute_bash, ready to be registered with the v2
 * `ToolEngine`. The `execute` returns a `ProcessSession` so the engine can
 * stream events and queue concurrency.
 */
export const executeBashTool: ToolDefinition = {
  ...executeBashSchema,
  getPolicy: () => getBashPolicy(),
  execute: async (
    args: unknown,
    context: ToolContext,
  ): Promise<ProcessSession> => {
    const { command } = args as { command: string };
    if (typeof command !== 'string' || command.trim().length === 0) {
      const session = new ProcessSession({
        executionId: `exec-bash-invalid-${Date.now()}`,
        command: '',
        args: [],
        cwd: process.cwd(),
      });
      session.error(new Error('executeBash: command must be a non-empty string'));
      return session;
    }

    const bashPath = process.env.TERMUX_BASH_PATH ?? DEFAULT_BASH_PATH;
    return processManager.run({
      command: bashPath,
      args: ['-c', command],
      cwd: process.cwd(),
      env: { ...process.env },
      signal: context.signal,
    });
  },
};
