import { spawn, type ChildProcess } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import type { ToolExecutionResult, ProcessOptions } from './types.js';
import './telemetry/ExecutionLogger.js';
import './telemetry/MetricsCollector.js';
import './replay/ReplayService.js';
import { ProcessSession } from './ProcessSession.js';
import { executionRegistry } from './ExecutionRegistry.js';

export interface RuntimeOptions {
  command: string;
  args: string[];
  cwd?: string;
  env?: Record<string, string | undefined>;
  maxExecutionTimeMs?: number;
  maxOutputBytes?: number;
  signal?: AbortSignal;
  traceId?: string;
  sessionId?: string;
  parentExecutionId?: string;
}

export class ProcessManager {
  async run(options: RuntimeOptions): Promise<ProcessSession> {
    const {
      command,
      args,
      cwd = process.cwd(),
      env,
      maxExecutionTimeMs = 45000,
      maxOutputBytes = 20 * 1024 * 1024,
      signal,
    } = options;

    const executionId = `exec-${randomUUID()}`;
    let child: ChildProcess | undefined;
    
    const session = new ProcessSession({
      executionId,
      command: options.command,
      args: options.args,
      cwd: options.cwd || process.cwd(),
      env: options.env,
      pauseStreams: () => {
        if (child?.stdout && !child.stdout.isPaused()) child.stdout.pause();
        if (child?.stderr && !child.stderr.isPaused()) child.stderr.pause();
      },
      resumeStreams: () => {
        if (child?.stdout && child.stdout.isPaused()) child.stdout.resume();
        if (child?.stderr && child.stderr.isPaused()) child.stderr.resume();
      },
      traceId: options.traceId,
      sessionId: options.sessionId,
      parentExecutionId: options.parentExecutionId,
    });

    return new Promise((resolve, reject) => {
      let timeoutId: NodeJS.Timeout | undefined;
      let sigtermTimeout: NodeJS.Timeout | undefined;
      let sigkillTimeout: NodeJS.Timeout | undefined;
      
      let outputByteCount = 0;
      let killedByTimeout = false;
      let killedByTruncation = false;
      let finished = false;
      let processExited = false;

      const cleanup = () => {
        if (timeoutId) clearTimeout(timeoutId);
        if (sigtermTimeout) clearTimeout(sigtermTimeout);
        if (sigkillTimeout) clearTimeout(sigkillTimeout);
        if (signal) signal.removeEventListener('abort', abortHandler);
      };

      const finish = () => {
        if (finished) return;
        finished = true;
        cleanup();
        resolve(session);
      };

      const killSequence = (reason: 'timeout' | 'truncation' | 'abort') => {
        if (reason === 'timeout') killedByTimeout = true;
        if (reason === 'truncation') killedByTruncation = true;
        
        if (processExited || session.isTerminal()) return;

        // الإيقاف الفوري لتدفق البيانات لحماية الذاكرة (Memory Shield)
        if (child?.stdout && !child.stdout.isPaused()) child.stdout.pause();
        if (child?.stderr && !child.stderr.isPaused()) child.stderr.pause();

        // تجنب إرسال إشارات لعملية ماتت بالفعل لتفادي أخطاء (ESRCH)
        if (child && !child.killed) {
          try {
            child.kill('SIGINT');
          } catch (e) { /* التجاهل الآمن */ }
        }

        sigtermTimeout = setTimeout(() => {
          if (!processExited && !session.isTerminal() && child && !child.killed) {
            try { child.kill('SIGTERM'); } catch (e) {}
          }
        }, 2000);

        sigkillTimeout = setTimeout(() => {
          if (!processExited && !session.isTerminal() && child && !child.killed) {
            try { child.kill('SIGKILL'); } catch (e) {}
          }
        }, 4000);
      };

      const abortHandler = () => killSequence('abort');

      try {
        child = spawn(command, args, {
          shell: false,
          cwd,
          env,
          stdio: 'pipe',
        });
      } catch (err) {
        session.error(err instanceof Error ? err : new Error(String(err)));
        finish();
        return;
      }

      if (typeof child.pid === 'number' && child.pid > 0) {
        session.start(child.pid);
      }

      child.stdout!.setEncoding('utf8');
      child.stderr!.setEncoding('utf8');

      timeoutId = setTimeout(() => {
        killSequence('timeout');
      }, maxExecutionTimeMs);

      signal?.addEventListener('abort', abortHandler, { once: true });
      if (signal?.aborted) abortHandler();

      const handleChunk = (chunk: string, isStderr: boolean) => {
        // حارس الذاكرة: منع استهلاك أي بيانات جديدة إذا بدأنا تسلسل الإعدام
        if (killedByTruncation || killedByTimeout || session.isTerminal()) return;

        outputByteCount += Buffer.byteLength(chunk, 'utf8');
        
        if (outputByteCount >= maxOutputBytes) {
          killedByTruncation = true;
          killSequence('truncation');
          return; // الخروج الفوري لمنع الإلحاق
        }

        if (isStderr) {
          session.appendStderr(chunk);
        } else {
          session.appendStdout(chunk);
        }
      };

      child.stdout!.on('data', (chunk: string) => handleChunk(chunk, false));
      child.stderr!.on('data', (chunk: string) => handleChunk(chunk, true));

      child.once('close', (code, signalName) => {
        processExited = true;
        if (!session.isTerminal()) {
          if (killedByTimeout) {
            session.timeout(signalName ?? null);
          } else {
            if (killedByTruncation) {
              session.markTruncated();
            }
            session.finish(code ?? null, signalName ?? null);
          }
        }
        finish();
      });

      child.once('error', (err) => {
        processExited = true;
        session.error(err);
        finish();
      });
    });
  }
}

export const processManager = new ProcessManager();

export async function runToCompletion(
  options: RuntimeOptions, 
  onStream?: (chunk: string, isStderr: boolean) => void
): Promise<ToolExecutionResult> {
  const session = await processManager.run(options);

  for await (const event of session.stream()) {
    if (onStream) {
      if (event.type === 'StdoutChunk' && 'chunk' in event) {
        onStream(event.chunk as string, false);
      } else if (event.type === 'StderrChunk' && 'chunk' in event) {
        onStream(event.chunk as string, true);
      } else if (event.type === 'StdoutBatch' && 'chunks' in event) {
        for (const chunk of (event.chunks as string[])) onStream(chunk, false);
      } else if (event.type === 'StderrBatch' && 'chunks' in event) {
        for (const chunk of (event.chunks as string[])) onStream(chunk, true);
      }
    }
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
