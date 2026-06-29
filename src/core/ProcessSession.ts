import type {
  ExecutionMetrics,
  ExecutionPolicy,
  ProcessSessionSnapshot,
  ProcessSessionStatus,
} from './types.js';
import type { ExecutionEventV3 } from './events/ExecutionEvent.js';
import { globalEventBus } from './events/EventBus.js';
import { RuntimeInvariantError } from './errors.js';

export interface ProcessSessionOptions {
  executionId: string;
  command: string;
  args: string[];
  cwd: string;
  env?: Record<string, string | undefined>;
  policy?: ExecutionPolicy;
  highWaterMark?: number;
  lowWaterMark?: number;
  pauseStreams?: () => void;
  resumeStreams?: () => void;
  traceId?: string;
  sessionId?: string;
  parentExecutionId?: string;
}

interface Deferred<T> {
  promise: Promise<T>;
  resolve: (value: T) => void;
  reject: (err: Error) => void;
}

interface StreamWaiter {
  resolve: (value: ExecutionEventV3) => void;
  reject: (err: Error) => void;
}

export class ProcessSession {
  readonly executionId: string;
  readonly command: string;
  readonly args: ReadonlyArray<string>;
  readonly cwd: string;
  readonly env: Record<string, string | undefined>;
  readonly policy: ExecutionPolicy | undefined;
  
  readonly traceId?: string;
  readonly sessionId?: string;
  readonly parentExecutionId?: string;

  pid: number | null = null;
  status: ProcessSessionStatus = 'queued';
  startedAt: number | null = null;
  endedAt: number | null = null;
  exitCode: number | null = null;
  signal: string | null = null;
  durationMs: number | null = null;
  timedOut: boolean = false;
  cancelled: boolean = false;
  truncated: boolean = false;
  stdoutBytes: number = 0;
  stderrBytes: number = 0;
  totalBytes: number = 0;
  metrics: ExecutionMetrics;

  private readonly eventQueue: ExecutionEventV3[] = [];
  private readonly streamWaiters: StreamWaiter[] = [];
  
  private sequenceNumber: number = 0;
  private readonly highWaterMark: number;
  private readonly lowWaterMark: number;
  private isPaused = false;
  private readonly pauseStreams: (() => void) | undefined;
  private readonly resumeStreams: (() => void) | undefined;

  private stdoutBuffer: string[] = [];
  private stdoutBufferBytes = 0;
  private stderrBuffer: string[] = [];
  private stderrBufferBytes = 0;
  private flushTimeout: NodeJS.Immediate | null = null;

  constructor(options: ProcessSessionOptions) {
    this.executionId = options.executionId;
    this.command = options.command;
    this.args = Object.freeze([...options.args]);
    this.cwd = options.cwd;
    this.env = { ...(options.env ?? {}) };
    this.policy = options.policy;
    this.highWaterMark = options.highWaterMark ?? 1024;
    this.lowWaterMark = options.lowWaterMark ?? 256;
    this.pauseStreams = options.pauseStreams;
    this.resumeStreams = options.resumeStreams;
    
    this.traceId = options.traceId;
    this.sessionId = options.sessionId;
    this.parentExecutionId = options.parentExecutionId;

    const queuedAt = Date.now();
    this.metrics = {
      queuedAt,
      stdoutBytes: 0,
      stderrBytes: 0,
      peakOutputRate: 0,
      terminationReason: 'unknown',
    };

    this.emit({
      type: 'SessionQueued',
      executionId: this.executionId,
      timestamp: Date.now(),
      sequenceNumber: this.nextSeq(),
      command: this.command,
      args: this.args,
      cwd: this.cwd,
    });
  }

  private nextSeq(): number {
    return ++this.sequenceNumber;
  }

  start(pid: number): void {
    if (this.status !== 'queued') {
      throw new RuntimeInvariantError(`ProcessSession ${this.executionId}: start() requires status 'queued', got '${this.status}'`);
    }
    if (!Number.isInteger(pid) || pid <= 0) {
      throw new Error(`ProcessSession ${this.executionId}: start() requires a positive integer pid, got ${pid}`);
    }
    const now = Date.now();
    this.pid = pid;
    this.startedAt = now;
    this.status = 'running';
    this.metrics.startedAt = now;
    if (this.metrics.queuedAt !== undefined) {
      this.metrics.waitTime = now - this.metrics.queuedAt;
    }
    this.emit({
      type: 'SessionStarted',
      executionId: this.executionId,
      timestamp: now,
      sequenceNumber: this.nextSeq(),
      pid,
    });
  }

  appendStdout(chunk: string): void {
    if (chunk.length === 0) return;
    const bytes = Buffer.byteLength(chunk, 'utf8');
    this.stdoutBytes += bytes;
    this.totalBytes += bytes;
    this.metrics.stdoutBytes = this.stdoutBytes;
    
    this.stdoutBuffer.push(chunk);
    this.stdoutBufferBytes += bytes;
    this.scheduleFlush();
  }

  appendStderr(chunk: string): void {
    if (chunk.length === 0) return;
    const bytes = Buffer.byteLength(chunk, 'utf8');
    this.stderrBytes += bytes;
    this.totalBytes += bytes;
    this.metrics.stderrBytes = this.stderrBytes;
    
    this.stderrBuffer.push(chunk);
    this.stderrBufferBytes += bytes;
    this.scheduleFlush();
  }

  private scheduleFlush(): void {
    if (this.isTerminal()) {
      throw new RuntimeInvariantError(`ProcessSession ${this.executionId}: Output appended to terminal session`);
    }
    if (!this.flushTimeout) {
      this.flushTimeout = setImmediate(() => this.flushBuffers());
    }
  }

  private flushBuffers(): void {
    if (this.flushTimeout) {
      clearImmediate(this.flushTimeout);
      this.flushTimeout = null;
    }
    
    if (this.stdoutBuffer.length > 0) {
      if (this.stdoutBuffer.length === 1) {
        this.emit({
          type: 'StdoutChunk',
          executionId: this.executionId,
          timestamp: Date.now(),
          sequenceNumber: this.nextSeq(),
          chunk: this.stdoutBuffer[0],
          bytes: this.stdoutBufferBytes,
        });
      } else {
        this.emit({
          type: 'StdoutBatch',
          executionId: this.executionId,
          timestamp: Date.now(),
          sequenceNumber: this.nextSeq(),
          chunks: Object.freeze([...this.stdoutBuffer]),
          bytes: this.stdoutBufferBytes,
        });
      }
      this.stdoutBuffer = [];
      this.stdoutBufferBytes = 0;
    }

    if (this.stderrBuffer.length > 0) {
      if (this.stderrBuffer.length === 1) {
        this.emit({
          type: 'StderrChunk',
          executionId: this.executionId,
          timestamp: Date.now(),
          sequenceNumber: this.nextSeq(),
          chunk: this.stderrBuffer[0],
          bytes: this.stderrBufferBytes,
        });
      } else {
        this.emit({
          type: 'StderrBatch',
          executionId: this.executionId,
          timestamp: Date.now(),
          sequenceNumber: this.nextSeq(),
          chunks: Object.freeze([...this.stderrBuffer]),
          bytes: this.stderrBufferBytes,
        });
      }
      this.stderrBuffer = [];
      this.stderrBufferBytes = 0;
    }
  }

  finish(exitCode: number | null, signal: string | null): void {
    this.assertCanTerminate('finish');
    this.flushBuffers();
    this.exitCode = exitCode;
    this.signal = signal;
    this.status = 'finished';
    const now = Date.now();
    this.endedAt = now;
    if (this.startedAt !== null) {
      this.durationMs = now - this.startedAt;
    }
    this.metrics.endedAt = now;
    this.metrics.runTime = this.durationMs ?? undefined;
    this.metrics.terminationReason = 'natural';
    this.emit({
      type: 'Completed',
      executionId: this.executionId,
      timestamp: now,
      sequenceNumber: this.nextSeq(),
      exitCode,
      signal,
      durationMs: this.durationMs ?? 0,
    });
  }

  cancel(signal: string | null): void {
    this.assertCanTerminate('cancel');
    this.flushBuffers();
    this.cancelled = true;
    this.signal = signal;
    this.status = 'cancelled';
    const now = Date.now();
    this.endedAt = now;
    if (this.startedAt !== null) {
      this.durationMs = now - this.startedAt;
    }
    this.metrics.endedAt = now;
    this.metrics.runTime = this.durationMs ?? undefined;
    this.metrics.terminationReason = 'cancelled';
    this.emit({
      type: 'Cancelled',
      executionId: this.executionId,
      timestamp: now,
      sequenceNumber: this.nextSeq(),
      reason: signal ?? 'user_aborted',
    });
  }

  timeout(signal: string | null): void {
    this.assertCanTerminate('timeout');
    this.flushBuffers();
    this.timedOut = true;
    this.signal = signal;
    this.status = 'error';
    const now = Date.now();
    this.endedAt = now;
    if (this.startedAt !== null) {
      this.durationMs = now - this.startedAt;
    }
    this.metrics.endedAt = now;
    this.metrics.runTime = this.durationMs ?? undefined;
    this.metrics.terminationReason = 'timeout';
    this.emit({
      type: 'Failed',
      executionId: this.executionId,
      timestamp: now,
      sequenceNumber: this.nextSeq(),
      error: 'Execution timed out',
      reason: 'timeout',
    });
  }

  error(error: Error | string): void {
    this.assertCanTerminate('error');
    this.flushBuffers();
    const message = error instanceof Error ? error.message : String(error);
    this.status = 'error';
    const now = Date.now();
    this.endedAt = now;
    if (this.startedAt !== null) {
      this.durationMs = now - this.startedAt;
    }
    this.metrics.endedAt = now;
    this.metrics.runTime = this.durationMs ?? undefined;
    this.metrics.terminationReason = 'error';
    this.emit({
      type: 'Failed',
      executionId: this.executionId,
      timestamp: now,
      sequenceNumber: this.nextSeq(),
      error: message,
      reason: 'error',
    });
  }

  markTruncated(): void {
    if (this.isTerminal()) return;
    this.flushBuffers();
    this.truncated = true;
    this.emit({
      type: 'Failed',
      executionId: this.executionId,
      timestamp: Date.now(),
      sequenceNumber: this.nextSeq(),
      error: 'Output truncated due to exceeding maxOutputBytes',
      reason: 'truncation',
    });
  }

  isRunning(): boolean {
    return this.status === 'running';
  }

  isTerminal(): boolean {
    return (
      this.status === 'finished' ||
      this.status === 'cancelled' ||
      this.status === 'error'
    );
  }

  snapshot(): ProcessSessionSnapshot {
    return {
      executionId: this.executionId,
      pid: this.pid,
      command: this.command,
      args: [...this.args],
      cwd: this.cwd,
      status: this.status,
      startedAt: this.startedAt,
      endedAt: this.endedAt,
      exitCode: this.exitCode,
      signal: this.signal,
      durationMs: this.durationMs,
      timedOut: this.timedOut,
      cancelled: this.cancelled,
      truncated: this.truncated,
      stdoutBytes: this.stdoutBytes,
      stderrBytes: this.stderrBytes,
      totalBytes: this.totalBytes,
      metrics: { ...this.metrics },
    };
  }

  stream(): AsyncGenerator<ExecutionEventV3, void, void> {
    return this.createStream();
  }

  private async *createStream(): AsyncGenerator<ExecutionEventV3, void, void> {
    while (true) {
      if (this.eventQueue.length > 0) {
        const event = this.eventQueue.shift() as ExecutionEventV3;
        
        // BACKPRESSURE: Check low watermark
        if (this.isPaused && this.eventQueue.length <= this.lowWaterMark) {
          this.isPaused = false;
          if (this.resumeStreams) this.resumeStreams();
        }

        yield event;
        if (this.isTerminal() && this.eventQueue.length === 0) {
          return;
        }
        continue;
      }
      if (this.isTerminal()) {
        return;
      }
      const deferred = this.createDeferred<ExecutionEventV3>();
      this.streamWaiters.push({
        resolve: deferred.resolve,
        reject: deferred.reject,
      });
      const event = await deferred.promise;
      yield event;
    }
  }

  private emit(event: any): void {
    if (this.isTerminal() && !['Completed', 'Failed', 'Cancelled'].includes(event.type)) {
      throw new RuntimeInvariantError(`ProcessSession ${this.executionId}: Cannot emit ${event.type} after session is terminal`);
    }

    const fullEvent: ExecutionEventV3 = {
      ...event,
      traceId: this.traceId,
      sessionId: this.sessionId,
      parentExecutionId: this.parentExecutionId,
    };
    Object.freeze(fullEvent);

    globalEventBus.emit(fullEvent);

    if (this.streamWaiters.length > 0) {
      for (const waiter of this.streamWaiters) {
        waiter.resolve(fullEvent);
      }
      this.streamWaiters.length = 0;
    } else {
      this.eventQueue.push(fullEvent);
      // BACKPRESSURE: Check high watermark
      if (!this.isPaused && this.eventQueue.length >= this.highWaterMark) {
        this.isPaused = true;
        if (this.pauseStreams) this.pauseStreams();
      }
    }
  }

  private createDeferred<T>(): Deferred<T> {
    let resolveFn!: (value: T) => void;
    let rejectFn!: (err: Error) => void;
    const promise = new Promise<T>((resolve, reject) => {
      resolveFn = resolve;
      rejectFn = reject;
    });
    return { promise, resolve: resolveFn, reject: rejectFn };
  }

  private assertCanTerminate(method: string): void {
    if (this.isTerminal()) {
      throw new RuntimeInvariantError(
        `ProcessSession ${this.executionId}: ${method}() called on terminal session (status='${this.status}')`,
      );
    }
  }
}
