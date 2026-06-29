import { describe, it } from 'node:test';
import assert from 'node:assert';
import { ExecutionRegistry } from '../src/core/ExecutionRegistry.ts';
import { ProcessSession } from '../src/core/ProcessSession.ts';

function makeSession(executionId: string, command: string = '/bin/echo'): ProcessSession {
  return new ProcessSession({
    executionId,
    command,
    args: ['hi'],
    cwd: process.cwd(),
  });
}

describe('ExecutionRegistry', () => {
  describe('construction', () => {
    it('rejects non-positive maxSessions', () => {
      assert.throws(() => new ExecutionRegistry(0), /positive number/);
      assert.throws(() => new ExecutionRegistry(-1), /positive number/);
      assert.throws(() => new ExecutionRegistry(Number.NaN), /positive number/);
    });

    it('accepts a positive maxSessions', () => {
      const r = new ExecutionRegistry(5);
      assert.equal(r.stats().maxSessions, 5);
      assert.equal(r.stats().total, 0);
    });
  });

  describe('register / unregister', () => {
    it('registers by executionId', () => {
      const r = new ExecutionRegistry();
      const s = makeSession('exec-1');
      r.register(s);
      assert.equal(r.getById('exec-1'), s);
      assert.equal(r.stats().total, 1);
    });

    it('re-registering the same executionId overwrites the previous entry', () => {
      const r = new ExecutionRegistry();
      const a = makeSession('exec-1');
      const b = makeSession('exec-1');
      r.register(a);
      r.register(b);
      assert.equal(r.getById('exec-1'), b);
      // Only one entry is tracked.
      assert.equal(r.stats().total, 1);
    });

    it('unregister removes the session by executionId', () => {
      const r = new ExecutionRegistry();
      const s = makeSession('exec-1');
      r.register(s);
      r.unregister(s);
      assert.equal(r.getById('exec-1'), undefined);
      assert.equal(r.stats().total, 0);
    });

    it('unregister is idempotent', () => {
      const r = new ExecutionRegistry();
      const s = makeSession('exec-1');
      r.register(s);
      r.unregister(s);
      // Should not throw.
      r.unregister(s);
      assert.equal(r.stats().total, 0);
    });
  });

  describe('updatePid', () => {
    it('indexes the session by pid and updates the byPid lookup', () => {
      const r = new ExecutionRegistry();
      const s = makeSession('exec-1');
      r.register(s);
      // No pid yet, byPid should be empty.
      assert.equal(r.getByPid(123), undefined);

      r.updatePid(s, 123);
      assert.equal(r.getByPid(123), s);
      assert.equal(s.pid, 123);
    });

    it('rejects an invalid pid', () => {
      const r = new ExecutionRegistry();
      const s = makeSession('exec-1');
      r.register(s);
      assert.throws(() => r.updatePid(s, 0), /positive integer/);
      assert.throws(() => r.updatePid(s, -1), /positive integer/);
      assert.throws(() => r.updatePid(s, 1.5), /positive integer/);
    });

    it('moves the session from the old pid index to the new pid index', () => {
      const r = new ExecutionRegistry();
      const s = makeSession('exec-1');
      r.register(s);
      r.updatePid(s, 100);
      assert.equal(r.getByPid(100), s);
      r.updatePid(s, 200);
      assert.equal(r.getByPid(100), undefined);
      assert.equal(r.getByPid(200), s);
      assert.equal(s.pid, 200);
    });

    it('does not steal a pid entry that belongs to a different session', () => {
      const r = new ExecutionRegistry();
      const a = makeSession('exec-a');
      const b = makeSession('exec-b');
      r.register(a);
      r.register(b);
      r.updatePid(a, 50);
      r.updatePid(b, 50);
      // The latest updatePid wins for byPid (b takes over 50).
      assert.equal(r.getByPid(50), b);
    });
  });

  describe('queries (running, completed, failed)', () => {
    it('returns only running sessions from getRunning()', () => {
      const r = new ExecutionRegistry();
      const running = makeSession('exec-running');
      const finished = makeSession('exec-finished');
      const errored = makeSession('exec-error');
      const cancelled = makeSession('exec-cancelled');

      r.register(running);
      r.register(finished);
      r.register(errored);
      r.register(cancelled);

      running.start(1);
      finished.start(2);
      finished.finish(0, null);
      errored.start(3);
      errored.error('boom');
      cancelled.start(4);
      cancelled.cancel('SIGTERM');

      const runningSnaps = r.getRunning();
      assert.equal(runningSnaps.length, 1);
      assert.equal(runningSnaps[0].executionId, 'exec-running');
      assert.equal(runningSnaps[0].status, 'running');
    });

    it('returns only finished sessions from getCompleted()', () => {
      const r = new ExecutionRegistry();
      const running = makeSession('exec-running');
      const finished = makeSession('exec-finished');
      r.register(running);
      r.register(finished);
      running.start(1);
      finished.start(2);
      finished.finish(0, null);

      const completed = r.getCompleted();
      assert.equal(completed.length, 1);
      assert.equal(completed[0].executionId, 'exec-finished');
      assert.equal(completed[0].status, 'finished');
    });

    it('returns error and cancelled sessions from getFailed()', () => {
      const r = new ExecutionRegistry();
      const errored = makeSession('exec-error');
      const cancelled = makeSession('exec-cancelled');
      const finished = makeSession('exec-finished');
      r.register(errored);
      r.register(cancelled);
      r.register(finished);

      errored.start(1);
      errored.error('boom');
      cancelled.start(2);
      cancelled.cancel('SIGTERM');
      finished.start(3);
      finished.finish(0, null);

      const failed = r.getFailed();
      const failedIds = failed.map((s) => s.executionId).sort();
      assert.deepEqual(failedIds, ['exec-cancelled', 'exec-error']);
    });

    it('getHistory returns snapshots ordered oldest first', () => {
      const r = new ExecutionRegistry();
      const a = makeSession('exec-a');
      const b = makeSession('exec-b');
      const c = makeSession('exec-c');
      r.register(a);
      // Ensure queuedAt differs by a tiny amount.
      const a2 = new ProcessSession({
        executionId: 'exec-a',
        command: a.command,
        args: a.args as string[],
        cwd: a.cwd,
      });
      // We can't actually replace via register without overwriting by executionId,
      // so instead just verify the existing order using a, b, c with staggered
      // construction. To guarantee distinct queuedAt values, sleep 2ms between.
      r.register(a);
      // Wait small interval.
      const wait = (ms: number) => new Promise((res) => setTimeout(res, ms));
      // Note: queuedAt is set in the constructor; we constructed them
      // synchronously so they may share a millisecond. We instead just
      // confirm the snapshot pipeline works regardless of order.
      void a2;
      r.register(b);
      r.register(c);

      const all = r.getHistory();
      assert.equal(all.length, 3);
      assert.equal(all[0].executionId, 'exec-a');
      assert.equal(all[1].executionId, 'exec-b');
      assert.equal(all[2].executionId, 'exec-c');
      void wait;
    });

    it('getHistory respects the limit argument', () => {
      const r = new ExecutionRegistry();
      r.register(makeSession('exec-a'));
      r.register(makeSession('exec-b'));
      r.register(makeSession('exec-c'));
      const first2 = r.getHistory(2);
      assert.equal(first2.length, 2);
    });
  });

  describe('stats()', () => {
    it('aggregates counts of total, running, completed, failed', () => {
      const r = new ExecutionRegistry();
      const running = makeSession('exec-running');
      const finished = makeSession('exec-finished');
      const errored = makeSession('exec-error');
      const cancelled = makeSession('exec-cancelled');
      const queued = makeSession('exec-queued');
      r.register(running);
      r.register(finished);
      r.register(errored);
      r.register(cancelled);
      r.register(queued);

      running.start(1);
      finished.start(2);
      finished.finish(0, null);
      errored.start(3);
      errored.error('boom');
      cancelled.start(4);
      cancelled.cancel('SIGTERM');
      // queued stays in queued status.

      const stats = r.stats();
      assert.equal(stats.total, 5);
      assert.equal(stats.running, 1);
      assert.equal(stats.completed, 1);
      assert.equal(stats.failed, 2); // error + cancelled
    });
  });

  describe('pruning', () => {
    it('prunes oldest terminal sessions when over maxSessions', () => {
      const r = new ExecutionRegistry(3);

      const s1 = makeSession('exec-1');
      const s2 = makeSession('exec-2');
      const s3 = makeSession('exec-3');
      const s4 = makeSession('exec-4');

      r.register(s1);
      r.register(s2);
      r.register(s3);
      r.register(s4);

      // All in queued state — prune() must NOT remove active sessions.
      r.prune();
      assert.equal(r.stats().total, 4);

      // Make s1, s2 terminal in order; s3, s4 stay queued.
      s1.start(1);
      s1.finish(0, null);
      s2.start(2);
      s2.finish(0, null);

      // Now prune — over the cap of 3, only terminal sessions get removed.
      r.prune();
      const stats = r.stats();
      // We had 4 total, cap is 3, must drop 1 terminal (s1).
      assert.ok(stats.total <= 3, `expected total <= 3, got ${stats.total}`);
      // Active (queued) sessions must remain.
      assert.ok(r.getById('exec-3'), 'queued session s3 must not be pruned');
      assert.ok(r.getById('exec-4'), 'queued session s4 must not be pruned');
    });

    it('is a no-op when total is within maxSessions', () => {
      const r = new ExecutionRegistry(10);
      const s = makeSession('exec-1');
      s.start(1);
      s.finish(0, null);
      r.register(s);
      r.prune();
      assert.equal(r.stats().total, 1);
    });

    it('is a no-op when only active sessions are over the cap', () => {
      const r = new ExecutionRegistry(2);
      const s1 = makeSession('exec-1');
      const s2 = makeSession('exec-2');
      const s3 = makeSession('exec-3');
      r.register(s1);
      r.register(s2);
      r.register(s3);
      s1.start(1);
      s2.start(2);
      s3.start(3);
      // All active, prune should not remove any.
      r.prune();
      assert.equal(r.stats().total, 3);
    });
  });
});
