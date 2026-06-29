import type {
  ExecutionEvent,
  ExecutionMetrics,
  ExecutionPolicy,
  ProcessSessionSnapshot,
  ProcessSessionStatus,
} from './types.js';

/**
 * Options used to construct a ProcessSession.
 */
export interface ProcessSessionOptions {
  executionId: string;
  command: string;
  args: string[];
  cwd: string;
  env?: Record<string, string | undefined>;
  policy?: ExecutionPolicy;
}

interface Deferred<T> {
  promise: Promise<T>;
  resolve: (value: T) => void;
  reject: (err: Error) => void;
}

interface StreamWaiter {
  resolve: (value: ExecutionEvent) => void;
  reject: (err: Error) => void;
}

/**
 * ProcessSession owns the lifecycle of a single execution.
 *
 * It does NOT retain stdout/stderr text; chunks are forwarded as events to
 * consumers and only byte counts are kept. This prevents memory blow-up when
 * many long-running executions are tracked simultaneously.
 */
export class ProcessSession {
  readonly executionId: string;
  readonly command: string;
  readonly args: ReadonlyArray<string>;
  readonly cwd: string;
  readonly env: Record<string, string | undefined>;
  readonly policy: ExecutionPolicy | undefined;

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

  private readonly eventQueue: ExecutionEvent[] = [];
  private readonly streamWaiters: StreamWaiter[] = [];
  private rateWindowStart: number | null = null;
  private rateWindowBytes: number = 0;

  constructor(options: ProcessSessionOptions) {
    this.executionId = options.executionId;
    this.command = options.command;
    this.args = Object.freeze([...options.args]);
    this.cwd = options.cwd;
    this.env = { ...(options.env ?? {}) };
    this.policy = options.policy;

    const queuedAt = Date.now();
    this.metrics = {
      queuedAt,
      stdoutBytes: 0,
      stderrBytes: 0,
      peakOutputRate: 0,
      terminationReason: 'unknown',
    };
  }

  /**
   * Mark the session as running and record the spawned child's PID.
   *
   * Can only be called while the session is in the `queued` status.
   */
  start(pid: number): void {
    if (this.status !== 'queued') {
      throw new Error(
        `ProcessSession ${this.executionId}: start() requires status 'queued', got '${this.status}'`,
      );
    }
    if (!Number.isInteger(pid) || pid <= 0) {
      throw new Error(
        `ProcessSession ${this.executionId}: start() requires a positive integer pid, got ${pid}`,
      );
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
      type: 'started',
      executionId: this.executionId,
      pid,
      startedAt: now,
    });
  }

  /**
   * Forward a stdout chunk from the child process.
   *
   * Emits a `stdout` event and updates byte counters and peak throughput.
   * The chunk text itself is NOT retained on the session.
   */
  appendStdout(chunk: string): void {
    if (chunk.length === 0) {
      return;
    }
    const bytes = Buffer.byteLength(chunk, 'utf8');
    this.stdoutBytes += bytes;
    this.totalBytes += bytes;
    this.metrics.stdoutBytes = this.stdoutBytes;
    this.recordOutput(bytes);
    this.emit({
      type: 'stdout',
      executionId: this.executionId,
      chunk,
      bytes,
    });
  }

  /**
   * Forward a stderr chunk from the child process.
   *
   * Emits a `stderr` event and updates byte counters and peak throughput.
   * The chunk text itself is NOT retained on the session.
   */
  appendStderr(chunk: string): void {
    if (chunk.length === 0) {
      return;
    }
    const bytes = Buffer.byteLength(chunk, 'utf8');
    this.stderrBytes += bytes;
    this.totalBytes += bytes;
    this.metrics.stderrBytes = this.stderrBytes;
    this.recordOutput(bytes);
    this.emit({
      type: 'stderr',
      executionId: this.executionId,
      chunk,
      bytes,
    });
  }

  /**
   * Mark the session as having exited normally (or via signal) and emit a
   * `finished` event. Can only be called once.
   */
  finish(exitCode: number | null, signal: string | null): void {
    this.assertCanTerminate('finish');
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
      type: 'finished',
      executionId: this.executionId,
      exitCode,
      signal,
      durationMs: this.durationMs ?? 0,
      totalBytes: this.totalBytes,
    });
  }

  /**
   * Mark the session as cancelled and emit a `cancelled` event. Can only be
   * called once.
   */
  cancel(signal: string | null): void {
    this.assertCanTerminate('cancel');
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
      type: 'cancelled',
      executionId: this.executionId,
      signal,
      durationMs: this.durationMs ?? 0,
    });
  }

  /**
   * Mark the session as having timed out and emit a `timeout` event. Can only
   * be called once.
   */
  timeout(signal: string | null): void {
    this.assertCanTerminate('timeout');
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
      type: 'timeout',
      executionId: this.executionId,
      signal,
      durationMs: this.durationMs ?? 0,
    });
  }

  /**
   * Mark the session as failed due to an internal error and emit an `error`
   * event. Can only be called once.
   */
  error(error: Error | string): void {
    this.assertCanTerminate('error');
    const message = error instanceof Error ? error.message : error;
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
      type: 'error',
      executionId: this.executionId,
      error: message,
    });
  }

  /**
   * Mark the session as having produced output that exceeded the configured
   * byte limit. No-op once the session is in a terminal status.
   */
  markTruncated(): void {
    if (this.isTerminal()) return;
    this.truncated = true;
  }

  /** True while the child process is running. */
  isRunning(): boolean {
    return this.status === 'running';
  }

  /** True once the session has reached a terminal status. */
  isTerminal(): boolean {
    return (
      this.status === 'finished' ||
      this.status === 'cancelled' ||
      this.status === 'error'
    );
  }

  /** Capture an immutable snapshot of the current session state. */
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

  /**
   * Consume the event stream for this session.
   *
   * Each invocation creates an independent consumer that observes every event
   * emitted from the moment the generator starts (including any events that
   * were already queued). The generator returns once the session reaches a
   * terminal status and all queued events have been delivered.
   */
  stream(): AsyncGenerator<ExecutionEvent, void, void> {
    return this.createStream();
  }

  private async *createStream(): AsyncGenerator<ExecutionEvent, void, void> {
    while (true) {
      if (this.eventQueue.length > 0) {
        const event = this.eventQueue.shift() as ExecutionEvent;
        yield event;
        if (this.isTerminal() && this.eventQueue.length === 0) {
          return;
        }
        continue;
      }
      if (this.isTerminal()) {
        return;
      }
      const deferred = this.createDeferred<ExecutionEvent>();
      this.streamWaiters.push({
        resolve: deferred.resolve,
        reject: deferred.reject,
      });
      const event = await deferred.promise;
      yield event;
      // Loop: more events may now be queued, or session may have terminated.
    }
  }

  private emit(event: ExecutionEvent): void {
    const waiter = this.streamWaiters.shift();
    if (waiter !== undefined) {
      waiter.resolve(event);
    } else {
      this.eventQueue.push(event);
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

  private recordOutput(bytes: number): void {
    const now = Date.now();
    if (this.rateWindowStart === null) {
      this.rateWindowStart = now;
      this.rateWindowBytes = 0;
    }
    this.rateWindowBytes += bytes;
    const elapsed = now - this.rateWindowStart;
    if (elapsed >= 1000) {
      const seconds = elapsed / 1000;
      const rate = this.rateWindowBytes / seconds;
      if (rate > this.metrics.peakOutputRate) {
        this.metrics.peakOutputRate = rate;
      }
      this.rateWindowStart = now;
      this.rateWindowBytes = 0;
    }
  }

  private assertCanTerminate(method: string): void {
    if (this.isTerminal()) {
      throw new Error(
        `ProcessSession ${this.executionId}: ${method}() called on terminal session (status='${this.status}')`,
      );
    }
  }
}
