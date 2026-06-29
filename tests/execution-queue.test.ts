import { describe, it } from 'node:test';
import assert from 'node:assert';
import { ExecutionQueue, CancellationError } from '../src/core/ExecutionQueue.ts';
import { ProcessSession } from '../src/core/ProcessSession.ts';
import type {
  ExecutionEvent,
  ProcessSessionSnapshot,
} from '../src/core/types.ts';

/**
 * Build a fake ProcessSession that records state transitions without
 * touching the filesystem. The session moves through queued -> running ->
 * finished/cancelled/timeout on demand.
 */
class FakeSession extends ProcessSession {
  public finishResolvers: Array<() => void> = [];
  private terminalKind: 'finished' | 'cancelled' | 'timeout' | null = null;

  constructor(executionId: string, command: string = '/bin/sleep') {
    super({ executionId, command, args: ['1'], cwd: process.cwd() });
  }

  begin(): void {
    this.start(Math.floor(Math.random() * 1_000_000) + 100000);
  }

  /**
   * Mark this fake session as finished and emit the appropriate terminal
   * event. Resolves once the terminal event has been emitted so the queue's
   * background drainer can observe it.
   */
  async endAs(kind: 'finished' | 'cancelled' | 'timeout'): Promise<void> {
    this.terminalKind = kind;
    switch (kind) {
      case 'finished':
        this.finish(0, null);
        break;
      case 'cancelled':
        this.cancel('SIGTERM');
        break;
      case 'timeout':
        this.timeout('SIGTERM');
        break;
    }
    // Yield so consumers can drain.
    await new Promise((r) => setImmediate(r));
  }

  isTerminalKind(): 'finished' | 'cancelled' | 'timeout' | null {
    return this.terminalKind;
  }
}

/**
 * Build a factory that returns a fake ProcessSession and exposes a handle
 * for the caller to advance the session to a terminal state.
 */
function makeFactory(): {
  factory: () => Promise<ProcessSession>;
  handle: FakeSession;
} {
  const handle = new FakeSession(`exec-fake-${Math.random().toString(36).slice(2)}`);
  const factory = async (): Promise<ProcessSession> => {
    handle.begin();
    return handle;
  };
  return { factory, handle };
}

/**
 * Drain a session to completion.
 */
async function drain(session: ProcessSession): Promise<ProcessSessionSnapshot> {
  for await (const _e of session.stream()) {
    // discard
  }
  return session.snapshot();
}

describe('ExecutionQueue', () => {
  describe('construction', () => {
    it('rejects non-positive maxConcurrency', () => {
      assert.throws(() => new ExecutionQueue(0), /positive integer/);
      assert.throws(() => new ExecutionQueue(-1), /positive integer/);
      assert.throws(() => new ExecutionQueue(1.5), /positive integer/);
    });

    it('accepts a positive integer maxConcurrency', () => {
      assert.ok(new ExecutionQueue(1));
      assert.ok(new ExecutionQueue(3));
      const q = new ExecutionQueue(2);
      assert.equal(q.stats().maxConcurrency, 2);
    });
  });

  describe('enqueue and simple execution', () => {
    it('dispatches a single item and resolves with its session', async () => {
      const queue = new ExecutionQueue(1);
      const { factory, handle } = makeFactory();

      const promise = queue.enqueue(factory);
      const session = await promise;
      assert.ok(session instanceof ProcessSession);
      assert.equal(session.status, 'running');
      assert.equal(queue.stats().running, 1);

      await handle.endAs('finished');
      // Wait for the queue's drainer to observe the terminal event.
      await new Promise((r) => setImmediate(r));
      assert.equal(queue.stats().running, 0);
    });

    it('passes queue-level executionId to waiting() until dispatched', async () => {
      const queue = new ExecutionQueue(1);
      const a = makeFactory();
      const b = makeFactory();
      // Start one immediately so the second waits.
      const firstPromise = queue.enqueue(a.factory);
      // Attach a catch handler immediately so a synchronous cancel() does
      // not produce an unhandled rejection.
      const secondPromise = queue.enqueue(b.factory).catch(() => undefined);
      // Yield so the queue's drain() runs and the first item transitions
      // from inflight to running.
      await new Promise((r) => setImmediate(r));
      const waitingIds = queue.waiting();
      // After dispatching the first, only the second should be waiting.
      assert.equal(waitingIds.length, 1);
      assert.equal(typeof waitingIds[0], 'string');
      // Cleanup.
      queue.cancel(waitingIds[0]);
      await a.handle.endAs('finished');
      await firstPromise;
      await secondPromise;
    });

    it('rejects the enqueue promise if the factory throws', async () => {
      const queue = new ExecutionQueue(1);
      const factory = async () => {
        throw new Error('factory boom');
      };
      await assert.rejects(
        () => queue.enqueue(factory),
        /factory boom/,
      );
      assert.equal(queue.stats().running, 0);
    });
  });

  describe('concurrency limit', () => {
    it('runs at most maxConcurrency items in parallel', async () => {
      const queue = new ExecutionQueue(2);
      const a = makeFactory();
      const b = makeFactory();
      const c = makeFactory();

      // Dispatch three: two should be running, one should be waiting.
      // Attach catch handlers immediately so the synchronous cancel below
      // does not produce an unhandled rejection.
      const pA = queue.enqueue(a.factory);
      const pB = queue.enqueue(b.factory);
      const pC = queue.enqueue(c.factory).catch(() => undefined);

      // Yield to let the queue dispatch the first two.
      await new Promise((r) => setImmediate(r));
      await new Promise((r) => setImmediate(r));

      assert.equal(queue.stats().running, 2);
      assert.equal(queue.stats().waiting, 1);

      // Cancel c so it doesn't get dispatched after we end a and b.
      queue.cancel(queue.waiting()[0]);

      await a.handle.endAs('finished');
      await b.handle.endAs('finished');

      await Promise.all([pA, pB, pC]);
    });

    it('dispatches the next waiting item when a running item finishes', async () => {
      const queue = new ExecutionQueue(2);
      const a = makeFactory();
      const b = makeFactory();
      const c = makeFactory();

      const pA = queue.enqueue(a.factory);
      const pB = queue.enqueue(b.factory);
      const pC = queue.enqueue(c.factory);

      await new Promise((r) => setImmediate(r));
      await new Promise((r) => setImmediate(r));

      assert.equal(queue.stats().running, 2);
      assert.equal(queue.stats().waiting, 1);

      // End a — c should be promoted from waiting to running.
      await a.handle.endAs('finished');
      // Allow queue's drainer to react.
      await new Promise((r) => setImmediate(r));
      await new Promise((r) => setImmediate(r));

      assert.equal(queue.stats().running, 2);
      assert.equal(queue.stats().waiting, 0);

      await b.handle.endAs('finished');
      await c.handle.endAs('finished');

      await Promise.all([pA, pB, pC]);
      assert.equal(queue.stats().running, 0);
    });
  });

  describe('priority ordering', () => {
    it('dispatches higher-priority items first', async () => {
      const queue = new ExecutionQueue(1);

      // Pre-fill the running slot with a long-running task.
      const blocker = makeFactory();
      const pBlocker = queue.enqueue(blocker.factory, { priority: 0 });

      // Yield so blocker is dispatched.
      await new Promise((r) => setImmediate(r));
      assert.equal(queue.stats().running, 1);

      // Enqueue three more items in order: low, high, mid.
      const low = makeFactory();
      const high = makeFactory();
      const mid = makeFactory();

      const pLow = queue.enqueue(low.factory, { priority: 1 });
      const pHigh = queue.enqueue(high.factory, { priority: 10 });
      const pMid = queue.enqueue(mid.factory, { priority: 5 });

      // waiting() should be ordered by priority desc: high, mid, low.
      const order = queue.waiting();
      assert.equal(order.length, 3);

      // End the blocker; the queue should now pick high, then mid, then low.
      await blocker.handle.endAs('finished');
      await new Promise((r) => setImmediate(r));
      assert.equal(queue.stats().running, 1);
      // The session now running should be the high-priority one.
      const runningNow = queue.running();
      assert.equal(runningNow.length, 1);
      assert.equal(runningNow[0].executionId, high.handle.executionId);

      await high.handle.endAs('finished');
      await new Promise((r) => setImmediate(r));
      assert.equal(queue.stats().running, 1);
      const runningNext = queue.running();
      assert.equal(runningNext[0].executionId, mid.handle.executionId);

      await mid.handle.endAs('finished');
      await new Promise((r) => setImmediate(r));
      const runningLast = queue.running();
      assert.equal(runningLast[0].executionId, low.handle.executionId);

      await low.handle.endAs('finished');

      await Promise.all([pBlocker, pLow, pHigh, pMid]);
    });

    it('preserves FIFO order within the same priority bucket', async () => {
      const queue = new ExecutionQueue(1);
      const blocker = makeFactory();
      const pBlocker = queue.enqueue(blocker.factory);
      await new Promise((r) => setImmediate(r));

      const x = makeFactory();
      const y = makeFactory();
      const z = makeFactory();

      const pX = queue.enqueue(x.factory, { priority: 5 });
      const pY = queue.enqueue(y.factory, { priority: 5 });
      const pZ = queue.enqueue(z.factory, { priority: 5 });

      // FIFO within priority: x, y, z.
      assert.deepEqual(queue.waiting().length, 3);

      await blocker.handle.endAs('finished');
      await new Promise((r) => setImmediate(r));
      assert.equal(queue.running()[0].executionId, x.handle.executionId);

      await x.handle.endAs('finished');
      await new Promise((r) => setImmediate(r));
      assert.equal(queue.running()[0].executionId, y.handle.executionId);

      await y.handle.endAs('finished');
      await new Promise((r) => setImmediate(r));
      assert.equal(queue.running()[0].executionId, z.handle.executionId);

      await z.handle.endAs('finished');
      await Promise.all([pBlocker, pX, pY, pZ]);
    });
  });

  describe('cancellation', () => {
    it('cancels a queued item and rejects its enqueue promise', async () => {
      const queue = new ExecutionQueue(1);
      const blocker = makeFactory();
      const pBlocker = queue.enqueue(blocker.factory);
      await new Promise((r) => setImmediate(r));

      const target = makeFactory();
      const pTarget = queue.enqueue(target.factory);
      await new Promise((r) => setImmediate(r));
      assert.equal(queue.stats().waiting, 1);

      const id = queue.waiting()[0];
      const ok = queue.cancel(id);
      assert.equal(ok, true);

      await assert.rejects(
        () => pTarget,
        (err: unknown) => err instanceof CancellationError && err.executionId === id,
      );
      assert.equal(queue.stats().waiting, 0);

      // Clean up the blocker.
      await blocker.handle.endAs('finished');
      await pBlocker;
    });

    it('cancels a running item via session.cancel()', async () => {
      const queue = new ExecutionQueue(1);
      const a = makeFactory();
      const pA = queue.enqueue(a.factory);
      await new Promise((r) => setImmediate(r));

      const ok = queue.cancel(a.handle.executionId);
      assert.equal(ok, true);

      const session = await pA;
      // The session itself should now be in the cancelled state.
      await drain(session);
      // Yield several ticks so the queue's drainSession can observe the
      // terminal status and remove the session from its runningItems map.
      for (let i = 0; i < 5; i += 1) {
        await new Promise((r) => setImmediate(r));
      }
      assert.equal(session.status, 'cancelled');
      assert.equal(queue.stats().running, 0);
    });

    it('returns false when no matching executionId is found', () => {
      const queue = new ExecutionQueue(1);
      assert.equal(queue.cancel('not-a-real-id'), false);
    });
  });

  describe('pause / resume', () => {
    it('pauses dispatch and resumes it on resume()', async () => {
      const queue = new ExecutionQueue(1);
      queue.pause();
      assert.equal(queue.stats().paused, true);

      const a = makeFactory();
      const pA = queue.enqueue(a.factory);

      // Even after yielding, the queue should not dispatch while paused.
      await new Promise((r) => setImmediate(r));
      await new Promise((r) => setImmediate(r));
      assert.equal(queue.stats().running, 0);
      assert.equal(queue.stats().waiting, 1);

      queue.resume();
      assert.equal(queue.stats().paused, false);
      await new Promise((r) => setImmediate(r));
      assert.equal(queue.stats().running, 1);

      await a.handle.endAs('finished');
      await pA;
    });

    it('running tasks finish even while paused', async () => {
      const queue = new ExecutionQueue(1);
      const a = makeFactory();
      const pA = queue.enqueue(a.factory);
      await new Promise((r) => setImmediate(r));
      assert.equal(queue.stats().running, 1);

      queue.pause();
      // The running task should still complete normally.
      await a.handle.endAs('finished');
      await pA;
      assert.equal(queue.stats().running, 0);
    });
  });

  describe('clear', () => {
    it('clears all waiting items and returns the count', async () => {
      const queue = new ExecutionQueue(1);
      const blocker = makeFactory();
      const pBlocker = queue.enqueue(blocker.factory);
      await new Promise((r) => setImmediate(r));

      const w1 = makeFactory();
      const w2 = makeFactory();
      const pW1 = queue.enqueue(w1.factory);
      const pW2 = queue.enqueue(w2.factory);
      await new Promise((r) => setImmediate(r));
      assert.equal(queue.stats().waiting, 2);

      const cleared = queue.clear();
      assert.equal(cleared, 2);
      assert.equal(queue.stats().waiting, 0);

      await assert.rejects(
        () => pW1,
        (err: unknown) => err instanceof CancellationError,
      );
      await assert.rejects(
        () => pW2,
        (err: unknown) => err instanceof CancellationError,
      );

      // The blocker should still finish normally.
      await blocker.handle.endAs('finished');
      await pBlocker;
    });

    it('returns 0 when there is nothing to clear', () => {
      const queue = new ExecutionQueue(2);
      assert.equal(queue.clear(), 0);
    });
  });

  describe('stats and accessors', () => {
    it('returns a snapshot of waiting and running items', async () => {
      const queue = new ExecutionQueue(2);
      const a = makeFactory();
      const b = makeFactory();
      const pA = queue.enqueue(a.factory);
      const pB = queue.enqueue(b.factory);
      await new Promise((r) => setImmediate(r));

      const stats = queue.stats();
      assert.equal(stats.maxConcurrency, 2);
      assert.equal(stats.running, 2);
      assert.equal(stats.waiting, 0);
      assert.equal(stats.paused, false);

      const running = queue.running();
      assert.equal(running.length, 2);

      await a.handle.endAs('finished');
      await b.handle.endAs('finished');
      await Promise.all([pA, pB]);
    });
  });
});

// Reference unused imports to satisfy noUnusedParameters rules in some tsconfigs.
void (null as unknown as ExecutionEvent);
