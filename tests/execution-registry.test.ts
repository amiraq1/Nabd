import { describe, it } from 'node:test';
import assert from 'node:assert';
import { ExecutionRegistry } from '../src/core/ExecutionRegistry.ts';
import { EventBus } from '../src/core/events/EventBus.ts';

function createRegistry(maxSessions?: number) {
  const bus = new EventBus();
  const r = new ExecutionRegistry(maxSessions, bus);
  return { r, bus };
}

function queueSession(bus: EventBus, executionId: string, timestamp: number = Date.now()) {
  bus.emit({
    type: 'SessionQueued',
    executionId,
    timestamp,
    sequenceNumber: 1,
    command: '/bin/echo',
    args: ['hi'],
    cwd: process.cwd(),
  });
}

function startSession(bus: EventBus, executionId: string, pid: number, timestamp: number = Date.now()) {
  bus.emit({
    type: 'SessionStarted',
    executionId,
    timestamp,
    sequenceNumber: 2,
    pid,
  });
}

function finishSession(bus: EventBus, executionId: string, timestamp: number = Date.now()) {
  bus.emit({
    type: 'Completed',
    executionId,
    timestamp,
    sequenceNumber: 3,
    exitCode: 0,
    signal: null,
    durationMs: 10,
  });
}

function errorSession(bus: EventBus, executionId: string, timestamp: number = Date.now()) {
  bus.emit({
    type: 'Failed',
    executionId,
    timestamp,
    sequenceNumber: 3,
    error: 'boom',
    reason: 'error',
  });
}

function cancelSession(bus: EventBus, executionId: string, timestamp: number = Date.now()) {
  bus.emit({
    type: 'Cancelled',
    executionId,
    timestamp,
    sequenceNumber: 3,
    reason: 'SIGTERM',
  });
}

describe('ExecutionRegistry (Event-Driven)', () => {
  describe('construction', () => {
    it('rejects non-positive maxSessions', () => {
      assert.throws(() => new ExecutionRegistry(0), /positive number/);
      assert.throws(() => new ExecutionRegistry(-1), /positive number/);
      assert.throws(() => new ExecutionRegistry(Number.NaN), /positive number/);
    });

    it('accepts a positive maxSessions', () => {
      const { r } = createRegistry(5);
      assert.equal(r.stats().maxSessions, 5);
      assert.equal(r.stats().total, 0);
    });
  });

  describe('SessionQueued', () => {
    it('registers by executionId on SessionQueued', () => {
      const { r, bus } = createRegistry();
      queueSession(bus, 'exec-1');
      assert.equal(r.getById('exec-1')?.executionId, 'exec-1');
      assert.equal(r.stats().total, 1);
    });
  });

  describe('SessionStarted', () => {
    it('indexes the session by pid and updates the byPid lookup', () => {
      const { r, bus } = createRegistry();
      queueSession(bus, 'exec-1');
      startSession(bus, 'exec-1', 123);
      assert.equal(r.getByPid(123)?.executionId, 'exec-1');
      assert.equal(r.getById('exec-1')?.pid, 123);
    });

    it('moves the session from the old pid index to the new pid index on restart', () => {
      const { r, bus } = createRegistry();
      queueSession(bus, 'exec-1');
      startSession(bus, 'exec-1', 100);
      assert.equal(r.getByPid(100)?.executionId, 'exec-1');
      startSession(bus, 'exec-1', 200);
      assert.equal(r.getByPid(100), undefined);
      assert.equal(r.getByPid(200)?.executionId, 'exec-1');
      assert.equal(r.getById('exec-1')?.pid, 200);
    });
  });

  describe('queries (running, completed, failed)', () => {
    it('returns only running sessions from getRunning()', () => {
      const { r, bus } = createRegistry();
      queueSession(bus, 'exec-running');
      queueSession(bus, 'exec-finished');
      queueSession(bus, 'exec-error');
      queueSession(bus, 'exec-cancelled');

      startSession(bus, 'exec-running', 1);
      startSession(bus, 'exec-finished', 2);
      finishSession(bus, 'exec-finished');
      startSession(bus, 'exec-error', 3);
      errorSession(bus, 'exec-error');
      startSession(bus, 'exec-cancelled', 4);
      cancelSession(bus, 'exec-cancelled');

      const runningSnaps = r.getRunning();
      assert.equal(runningSnaps.length, 1);
      assert.equal(runningSnaps[0].executionId, 'exec-running');
      assert.equal(runningSnaps[0].status, 'running');
    });

    it('returns only finished sessions from getCompleted()', () => {
      const { r, bus } = createRegistry();
      queueSession(bus, 'exec-running');
      queueSession(bus, 'exec-finished');
      startSession(bus, 'exec-running', 1);
      startSession(bus, 'exec-finished', 2);
      finishSession(bus, 'exec-finished');

      const completed = r.getCompleted();
      assert.equal(completed.length, 1);
      assert.equal(completed[0].executionId, 'exec-finished');
      assert.equal(completed[0].status, 'finished');
    });

    it('returns error and cancelled sessions from getFailed()', () => {
      const { r, bus } = createRegistry();
      queueSession(bus, 'exec-error');
      queueSession(bus, 'exec-cancelled');
      queueSession(bus, 'exec-finished');

      startSession(bus, 'exec-error', 1);
      errorSession(bus, 'exec-error');
      startSession(bus, 'exec-cancelled', 2);
      cancelSession(bus, 'exec-cancelled');
      startSession(bus, 'exec-finished', 3);
      finishSession(bus, 'exec-finished');

      const failed = r.getFailed();
      const failedIds = failed.map((s) => s.executionId).sort();
      assert.deepEqual(failedIds, ['exec-cancelled', 'exec-error']);
    });

    it('getHistory returns snapshots ordered oldest first', () => {
      const { r, bus } = createRegistry();
      queueSession(bus, 'exec-a', 10);
      queueSession(bus, 'exec-b', 20);
      queueSession(bus, 'exec-c', 30);

      const all = r.getHistory();
      assert.equal(all.length, 3);
      assert.equal(all[0].executionId, 'exec-a');
      assert.equal(all[1].executionId, 'exec-b');
      assert.equal(all[2].executionId, 'exec-c');
    });

    it('getHistory respects the limit argument', () => {
      const { r, bus } = createRegistry();
      queueSession(bus, 'exec-a', 10);
      queueSession(bus, 'exec-b', 20);
      queueSession(bus, 'exec-c', 30);
      const first2 = r.getHistory(2);
      assert.equal(first2.length, 2);
    });
  });

  describe('stats()', () => {
    it('aggregates counts of total, running, completed, failed', () => {
      const { r, bus } = createRegistry();
      queueSession(bus, 'exec-running');
      queueSession(bus, 'exec-finished');
      queueSession(bus, 'exec-error');
      queueSession(bus, 'exec-cancelled');
      queueSession(bus, 'exec-queued');

      startSession(bus, 'exec-running', 1);
      startSession(bus, 'exec-finished', 2);
      finishSession(bus, 'exec-finished');
      startSession(bus, 'exec-error', 3);
      errorSession(bus, 'exec-error');
      startSession(bus, 'exec-cancelled', 4);
      cancelSession(bus, 'exec-cancelled');

      const stats = r.stats();
      assert.equal(stats.total, 5);
      assert.equal(stats.running, 1);
      assert.equal(stats.completed, 1);
      assert.equal(stats.failed, 2); // error + cancelled
    });
  });

  describe('pruning', () => {
    it('prunes oldest terminal sessions when over maxSessions', () => {
      const { r, bus } = createRegistry(3);

      queueSession(bus, 'exec-1', 10);
      queueSession(bus, 'exec-2', 20);
      queueSession(bus, 'exec-3', 30);
      queueSession(bus, 'exec-4', 40);

      // All in queued state — prune() must NOT remove active sessions.
      r.prune();
      assert.equal(r.stats().total, 4);

      // Make s1, s2 terminal in order; s3, s4 stay queued.
      startSession(bus, 'exec-1', 1, 15);
      finishSession(bus, 'exec-1', 25); // triggers prune
      
      startSession(bus, 'exec-2', 2, 25);
      finishSession(bus, 'exec-2', 35); // triggers prune

      const stats = r.stats();
      // We had 4 total, cap is 3, must drop 1 terminal (s1).
      assert.ok(stats.total <= 3, `expected total <= 3, got ${stats.total}`);
      // Active (queued) sessions must remain.
      assert.ok(r.getById('exec-3'), 'queued session s3 must not be pruned');
      assert.ok(r.getById('exec-4'), 'queued session s4 must not be pruned');
    });

    it('is a no-op when total is within maxSessions', () => {
      const { r, bus } = createRegistry(10);
      queueSession(bus, 'exec-1', 10);
      startSession(bus, 'exec-1', 1, 20);
      finishSession(bus, 'exec-1', 30); // auto prunes
      assert.equal(r.stats().total, 1);
    });

    it('is a no-op when only active sessions are over the cap', () => {
      const { r, bus } = createRegistry(2);
      queueSession(bus, 'exec-1', 10);
      queueSession(bus, 'exec-2', 20);
      queueSession(bus, 'exec-3', 30);
      
      startSession(bus, 'exec-1', 1, 15);
      startSession(bus, 'exec-2', 2, 25);
      startSession(bus, 'exec-3', 3, 35);
      
      r.prune();
      assert.equal(r.stats().total, 3);
    });
  });
});
