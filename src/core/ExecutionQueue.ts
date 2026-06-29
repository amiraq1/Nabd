import type { ProcessSession } from './ProcessSession.js';

/**
 * Thrown by `ExecutionQueue` when a queued execution is cancelled before it
 * has been started.
 *
 * The associated `executionId` is the queue-level identifier assigned to the
 * item at enqueue time. Use it to correlate the rejection back to the
 * original `enqueue()` call (the user-visible `ProcessSession` is never
 * produced for cancelled-while-waiting items).
 */
export class CancellationError extends Error {
  readonly executionId: string;

  constructor(executionId: string, reason: string) {
    super(reason);
    this.name = 'CancellationError';
    this.executionId = executionId;
  }
}

/**
 * Options accepted by `ExecutionQueue.enqueue()`.
 */
export interface QueueOptions {
  /**
   * Priority bucket for this item. Higher values run first; items with the
   * same priority run in FIFO order. Defaults to `0`.
   */
  priority?: number;
}

/**
 * Snapshot of the queue's current state, returned by `stats()`.
 */
export interface QueueStatistics {
  /** Number of items waiting for an execution slot. */
  waiting: number;
  /** Number of items currently running. */
  running: number;
  /** Configured maximum concurrent executions. */
  maxConcurrency: number;
  /** `true` if the queue is paused and not dispatching new items. */
  paused: boolean;
}

interface Deferred<T> {
  promise: Promise<T>;
  resolve: (value: T) => void;
  reject: (err: Error) => void;
}

interface WaitingItem {
  executionId: string;
  factory: () => Promise<ProcessSession>;
  priority: number;
  enqueuedAt: number;
  deferred: Deferred<ProcessSession>;
}

interface RunningItem {
  session: ProcessSession;
}

/**
 * FIFO + priority queue that schedules concurrent `ProcessSession`
 * executions.
 *
 * Ordering: items with higher `priority` run first; items with the same
 * priority are dispatched in FIFO order. Concurrency is capped at
 * `maxConcurrency`; additional items wait until a running session reaches a
 * terminal state.
 *
 * Cancellation:
 * - A waiting item is removed and its enqueue promise is rejected with
 *   `CancellationError`.
 * - A running session is cancelled by invoking its own `cancel()` method;
 *   the queue tracks running sessions by their `ProcessSession.executionId`.
 *
 * Pause / resume: `pause()` prevents dispatch of new items while leaving
 * running items to complete. `resume()` clears the flag and immediately
 * attempts to dispatch waiting items.
 *
 * Stream draining: once a session starts, the queue attaches a background
 * consumer to `session.stream()` to ensure queued events flow (otherwise a
 * session whose `stream()` is never read would deadlock against its own
 * internal waiters). The drained events are discarded; consumers that need
 * the events should iterate the stream themselves — multiple consumers are
 * supported by `ProcessSession`.
 */
export class ExecutionQueue {
  private readonly maxConcurrency: number;
  private readonly waitingItems: WaitingItem[] = [];
  private readonly runningItems: Map<string, RunningItem> = new Map();
  // Tracks items whose factory has been invoked but whose returned promise
  // has not yet resolved (i.e. the session has not yet been added to
  // `runningItems`). Without this counter, `drain()` would dispatch every
  // queued item in a single synchronous loop because
  // `factory().then()` only fires on a microtask, after the while loop has
  // already finished iterating.
  private inflightDispatches = 0;
  private paused = false;
  private nextId = 0;

  /**
   * Construct a queue with the given concurrency limit.
   *
   * @param maxConcurrency - Maximum concurrent executions (default `3`).
   * @throws Error if `maxConcurrency` is not a positive integer.
   */
  constructor(maxConcurrency: number = 3) {
    if (!Number.isInteger(maxConcurrency) || maxConcurrency < 1) {
      throw new Error(
        `ExecutionQueue: maxConcurrency must be a positive integer, got ${maxConcurrency}`,
      );
    }
    this.maxConcurrency = maxConcurrency;
  }

  /**
   * Enqueue a factory that returns a `ProcessSession`.
   *
   * The returned promise resolves with the session once the queue has
   * dispatched it. If the item is cancelled while waiting (via `cancel()` or
   * `clear()`), the promise rejects with `CancellationError`. If the factory
   * itself throws, the promise rejects with the factory's error.
   *
   * @param factory - Async factory invoked when an execution slot is free.
   * @param options - Optional priority and other dispatch hints.
   */
  enqueue(
    factory: () => Promise<ProcessSession>,
    options: QueueOptions = {},
  ): Promise<ProcessSession> {
    const priority = options.priority ?? 0;
    const executionId = this.generateExecutionId();
    const deferred = this.createDeferred<ProcessSession>();
    const item: WaitingItem = {
      executionId,
      factory,
      priority,
      enqueuedAt: Date.now(),
      deferred,
    };
    this.insertSorted(item);
    this.drain();
    return deferred.promise;
  }

  /**
   * Cancel a queued or running task by executionId.
   *
   * For a waiting item, the item is removed from the queue and its enqueue
   * promise is rejected with `CancellationError`. For a running session,
   * `session.cancel('SIGTERM')` is invoked. If `executionId` matches neither
   * a waiting nor a running entry, returns `false`.
   *
   * Note: queued items are identified by the queue-level id returned via
   * `waiting()`. Running items are identified by the `executionId` of the
   * resolved `ProcessSession`.
   *
   * @returns `true` if a matching entry was found and acted on.
   */
  cancel(executionId: string): boolean {
    const waitingIndex = this.waitingItems.findIndex(
      (w) => w.executionId === executionId,
    );
    if (waitingIndex >= 0) {
      const [item] = this.waitingItems.splice(waitingIndex, 1);
      item.deferred.reject(
        new CancellationError(item.executionId, 'execution cancelled'),
      );
      return true;
    }
    const running = this.runningItems.get(executionId);
    if (running !== undefined) {
      if (!running.session.isTerminal()) {
        running.session.cancel('SIGTERM');
      }
      return true;
    }
    return false;
  }

  /**
   * Pause dequeuing of new tasks. Items already running continue to
   * completion; once they finish, their slots are not refilled until
   * `resume()` is called.
   */
  pause(): void {
    this.paused = true;
  }

  /**
   * Resume dequeuing. Pending waiting items are dispatched immediately up to
   * `maxConcurrency`.
   */
  resume(): void {
    this.paused = false;
    this.drain();
  }

  /**
   * Clear all waiting tasks. Their enqueue promises are rejected with
   * `CancellationError`. Running tasks are not affected.
   *
   * @returns The number of waiting tasks that were cleared.
   */
  clear(): number {
    const count = this.waitingItems.length;
    for (const item of this.waitingItems) {
      item.deferred.reject(
        new CancellationError(item.executionId, 'queue cleared'),
      );
    }
    this.waitingItems.length = 0;
    return count;
  }

  /**
   * Get currently running sessions.
   *
   * @returns A new array of running `ProcessSession` instances. The returned
   * array is a copy and may be mutated by the caller.
   */
  running(): ProcessSession[] {
    const result: ProcessSession[] = [];
    for (const item of this.runningItems.values()) {
      result.push(item.session);
    }
    return result;
  }

  /**
   * Get the queue-level executionIds of waiting items, in dispatch order
   * (highest priority first, FIFO within the same priority).
   *
   * @returns A new array of executionId strings.
   */
  waiting(): string[] {
    return this.waitingItems.map((w) => w.executionId);
  }

  /**
   * Get a snapshot of queue statistics.
   */
  stats(): QueueStatistics {
    return {
      waiting: this.waitingItems.length,
      running: this.runningItems.size,
      maxConcurrency: this.maxConcurrency,
      paused: this.paused,
    };
  }

  // ---------------- private helpers ----------------

  private generateExecutionId(): string {
    this.nextId += 1;
    return `eq-${Date.now().toString(36)}-${this.nextId.toString(36)}`;
  }

  private insertSorted(item: WaitingItem): void {
    // Higher priority first. V8's sort is stable (since Node 12), so FIFO is
    // preserved within the same priority bucket.
    this.waitingItems.push(item);
    this.waitingItems.sort((a, b) => b.priority - a.priority);
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

  private drain(): void {
    while (
      !this.paused &&
      this.runningItems.size + this.inflightDispatches < this.maxConcurrency
    ) {
      const item = this.waitingItems.shift();
      if (item === undefined) {
        return;
      }
      this.inflightDispatches += 1;
      this.startItem(item);
    }
  }

  private startItem(item: WaitingItem): void {
    // Promise.then() always defers, even for already-resolved promises, so
    // dispatch is safe even if the factory resolves synchronously.
    item
      .factory()
      .then((session) => {
        this.inflightDispatches -= 1;
        this.runningItems.set(session.executionId, { session });
        item.deferred.resolve(session);
        void this.drainSession(session);
      })
      .catch((err: unknown) => {
        this.inflightDispatches -= 1;
        const error =
          err instanceof Error
            ? err
            : new Error(`factory threw: ${String(err)}`);
        item.deferred.reject(error);
        // Free the slot and try the next item.
        this.drain();
      });
  }

  private async drainSession(session: ProcessSession): Promise<void> {
    try {
      const stream = session.stream();
      // Pull events until the stream completes (session is terminal and the
      // internal queue is drained). Events are discarded here; consumers
      // that care about them should iterate `session.stream()` themselves.
      while (true) {
        const result = await stream.next();
        if (result.done === true) {
          break;
        }
      }
    } catch {
      // The stream may reject if the underlying generator is torn down; the
      // slot is freed in the finally block either way.
    } finally {
      this.runningItems.delete(session.executionId);
      this.drain();
    }
  }
}
