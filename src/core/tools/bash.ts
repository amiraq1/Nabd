import { randomUUID } from 'node:crypto';
import type {
  ToolContext,
  ToolDefinition,
  ExecutionPolicy,
  ToolExecutionResult,
  ToolStreamHandler,
} from '../types.js';
import { ProcessSession } from '../ProcessSession.js';
import { processManager } from '../process-manager.js';

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

const DEFAULT_BASH_PATH = '/data/data/com.termux/files/usr/bin/bash';

/**
 * قائمة سوداء لمتغيرات البيئة الحساسة لحماية بيئة التنفيذ في Termux.
 */
const DANGEROUS_ENV_KEYS = new Set(['LD_PRELOAD', 'LD_LIBRARY_PATH', 'PROMPT_COMMAND']);

/**
 * دالة مساعدة لتنظيف متغيرات البيئة قبل تمريرها للعملية الفرعية.
 */
function getSanitizedEnv(): Record<string, string | undefined> {
  const safeEnv = { ...process.env };
  for (const key of DANGEROUS_ENV_KEYS) {
    delete safeEnv[key];
  }
  return safeEnv;
}

export function getBashPolicy(): Partial<ExecutionPolicy> {
  return {
    maxExecutionTimeMs: 45000,
    maxOutputBytes: 20 * 1024 * 1024,
    allowNetwork: true,
    allowFilesystemWrite: true,
    allowDelete: false,
    allowBackgroundProcess: false,
    workingDirectory: process.cwd(),
    environment: getSanitizedEnv(),
    allowedCommands: [], // يُسمح بكل الأوامر داخلياً لأن المفتش الأمني الخارجي يعالج السياسات
  };
}

export async function executeBash(
  command: string,
  onStream?: ToolStreamHandler,
  signal?: AbortSignal,
): Promise<ToolExecutionResult> {
  if (typeof command !== 'string' || command.trim().length === 0) {
    throw new Error('فشل التنفيذ: يجب أن يكون الأمر النصي (command) صالحاً وغير فارغ.');
  }

  const bashPath = process.env.TERMUX_BASH_PATH ?? DEFAULT_BASH_PATH;
  const options = {
    command: bashPath,
    args: ['-c', command],
    cwd: process.cwd(),
    env: getSanitizedEnv(),
    signal,
  };

  const session = await processManager.run(options);

  if (onStream) {
    (async () => {
      try {
        for await (const event of session.stream()) {
          if (event.type === 'StdoutChunk' && 'chunk' in event) onStream(event.chunk as string, false);
          if (event.type === 'StderrChunk' && 'chunk' in event) onStream(event.chunk as string, true);
          if (event.type === 'StdoutBatch' && 'chunks' in event) {
            for (const chunk of event.chunks as string[]) onStream(chunk, false);
          }
          if (event.type === 'StderrBatch' && 'chunks' in event) {
            for (const chunk of event.chunks as string[]) onStream(chunk, true);
          }
        }
      } catch (err) {
        console.warn(`[executeBash] انقطع تدفق البيانات أو أُغلقت العملية قسرياً للأمر: ${command}`, err);
      }
    })();
  }

  // التفريغ (Draining) لضمان الوصول لحالة النهاية قبل بناء نتيجة التنفيذ
  try {
    for await (const _event of session.stream()) {
      // no-op
    }
  } catch (err) {
    // التقاط آمن في حالة تحطم الـ stream أثناء التفريغ
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

export const executeBashTool: ToolDefinition = {
  ...executeBashSchema,
  id: 'tool-execute_bash-v1',
  version: '1.0.0',
  category: 'system',
  visibility: 'stable',
  permissions: ['system', 'filesystem', 'dangerous'],
  aliases: ['bash', 'shell', 'terminal'],
  getPolicy: () => getBashPolicy(),
  execute: async (
    args: unknown,
    context: ToolContext,
  ): Promise<ProcessSession> => {
    const { command } = args as { command?: string };
    
    if (typeof command !== 'string' || command.trim().length === 0) {
      const session = new ProcessSession({
        executionId: `exec-bash-invalid-${randomUUID()}`,
        command: 'bash',
        args: [],
        cwd: process.cwd(),
      });
      session.error(new Error('فشل التنفيذ: يجب أن يكون الأمر النصي (command) صالحاً وغير فارغ.'));
      return session;
    }

    const bashPath = process.env.TERMUX_BASH_PATH ?? DEFAULT_BASH_PATH;
    return processManager.run({
      command: bashPath,
      args: ['-c', command],
      cwd: process.cwd(),
      env: getSanitizedEnv(),
      signal: context.signal, // الربط المباشر مع سياق الإلغاء
    });
  },
};
