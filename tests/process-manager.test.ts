import { describe, it } from 'node:test';
import assert from 'node:assert';
import { ProcessManager } from '../src/core/process-manager.ts';
import type { ProcessSession } from '../src/core/ProcessSession.ts';
import type {
  ExecutionEvent,
  ProcessSessionSnapshot,
} from '../src/core/types.ts';

type StreamChunk = { chunk: string; isStderr: boolean };

/**
 * Drive a session to a terminal state by iterating its event stream.
 * Returns the aggregated stream chunks and the final snapshot.
 */
async function runSession(
  session: ProcessSession,
): Promise<{ chunks: StreamChunk[]; snapshot: ProcessSessionSnapshot }> {
  const chunks: StreamChunk[] = [];
  for await (const event of session.stream()) {
    if (event.type === 'stdout') {
      chunks.push({ chunk: event.chunk, isStderr: false });
    } else if (event.type === 'stderr') {
      chunks.push({ chunk: event.chunk, isStderr: true });
    }
  }
  return { chunks, snapshot: session.snapshot() };
}

describe('ProcessManager', () => {
  it('runs echo and captures stdout with exit code 0', async () => {
    const pm = new ProcessManager();
    const session = await pm.run({
      command: '/bin/echo',
      args: ['hello'],
    });

    const { chunks, snapshot } = await runSession(session);

    assert.equal(snapshot.exitCode, 0);
    assert.equal(snapshot.signal, null);
    assert.equal(snapshot.timedOut, false);
    assert.equal(snapshot.truncated, false);
    assert.equal(snapshot.cancelled, false);
    assert.equal(snapshot.status, 'finished');

    const stdout = chunks.filter((c) => !c.isStderr).map((c) => c.chunk).join('');
    const stderr = chunks.filter((c) => c.isStderr).map((c) => c.chunk).join('');
    assert.match(stdout, /hello/);
    assert.equal(stderr, '');
  });

  it('reports stderr chunks with isStderr=true and exit code 0', async () => {
    const pm = new ProcessManager();
    const session = await pm.run({
      command: '/bin/bash',
      args: ['-c', 'echo error >&2'],
    });

    const { chunks, snapshot } = await runSession(session);

    assert.equal(snapshot.exitCode, 0);
    assert.equal(snapshot.signal, null);
    assert.equal(snapshot.timedOut, false);
    assert.equal(snapshot.truncated, false);
    assert.equal(snapshot.status, 'finished');

    const stderrChunks = chunks.filter((c) => c.isStderr);
    assert.ok(stderrChunks.length > 0, 'expected at least one stderr chunk');
    const stderrText = stderrChunks.map((c) => c.chunk).join('');
    assert.match(stderrText, /error/);

    const stdoutChunks = chunks.filter((c) => !c.isStderr);
    assert.deepEqual(stdoutChunks, []);
  });

  it('propagates non-zero exit code from the child process', async () => {
    const pm = new ProcessManager();
    const session = await pm.run({
      command: '/bin/bash',
      args: ['-c', 'exit 42'],
    });

    const { chunks, snapshot } = await runSession(session);

    assert.equal(snapshot.exitCode, 42);
    assert.equal(snapshot.signal, null);
    assert.equal(snapshot.timedOut, false);
    assert.equal(snapshot.truncated, false);
    assert.equal(snapshot.status, 'finished');
    assert.deepEqual(chunks, []);
  });

  it('times out long-running commands and reports a kill signal', async () => {
    const pm = new ProcessManager();
    const session = await pm.run({
      command: '/bin/sleep',
      args: ['10'],
      maxExecutionTimeMs: 100,
    });

    const { snapshot } = await runSession(session);

    assert.equal(snapshot.timedOut, true);
    assert.equal(snapshot.truncated, false);
    assert.equal(snapshot.cancelled, false);
    assert.equal(snapshot.status, 'error');
    // The kill sequence escalates SIGINT -> SIGTERM -> SIGKILL. Depending on
    // which signal the OS delivered first, any of those is acceptable.
    assert.ok(
      ['SIGINT', 'SIGTERM', 'SIGKILL'].includes(snapshot.signal as string),
      `expected signal to be SIGINT/SIGTERM/SIGKILL, got ${snapshot.signal}`,
    );
    assert.ok(
      snapshot.durationMs !== null && snapshot.durationMs >= 100,
      'should have run for at least the timeout',
    );
  });

  it('aborts on external signal without flagging timedOut', async () => {
    const pm = new ProcessManager();
    const abortController = new AbortController();

    const promise = pm.run({
      command: '/bin/sleep',
      args: ['10'],
      signal: abortController.signal,
    });

    setTimeout(() => abortController.abort(), 50);

    const session = await promise;
    const { snapshot } = await runSession(session);

    // External aborts do not trip the execution-timeout path; they go
    // through the cancel/finish code path. `timedOut` must remain false.
    assert.equal(snapshot.timedOut, false);
    assert.equal(snapshot.truncated, false);
    // Process should have been terminated by one of the kill signals.
    assert.ok(
      ['SIGINT', 'SIGTERM', 'SIGKILL'].includes(snapshot.signal as string),
      `expected signal to be SIGINT/SIGTERM/SIGKILL, got ${snapshot.signal}`,
    );
    assert.ok(
      snapshot.status === 'finished' ||
        snapshot.status === 'cancelled' ||
        snapshot.status === 'error',
      `expected terminal status, got ${snapshot.status}`,
    );
  });

  it('truncates output that exceeds maxOutputBytes', async () => {
    const pm = new ProcessManager();
    const session = await pm.run({
      command: '/usr/bin/seq',
      args: ['1', '1000000'],
      maxOutputBytes: 100,
    });

    const { snapshot } = await runSession(session);

    assert.equal(snapshot.truncated, true);
    assert.equal(snapshot.timedOut, false);
    // Process should have been killed (truncated) by one of the kill signals.
    assert.ok(
      snapshot.signal === null ||
        ['SIGINT', 'SIGTERM', 'SIGKILL'].includes(snapshot.signal as string),
      `expected signal null or kill signal, got ${snapshot.signal}`,
    );
  });

  it('reports null exit code and null signal when spawn fails', async () => {
    const pm = new ProcessManager();
    const session = await pm.run({
      command: '/definitely/not/real',
      args: [],
    });

    const { snapshot } = await runSession(session);

    assert.equal(snapshot.exitCode, null);
    assert.equal(snapshot.signal, null);
    assert.equal(snapshot.timedOut, false);
    assert.equal(snapshot.truncated, false);
    assert.equal(snapshot.status, 'error');
  });

  it('escalates to SIGKILL when the process ignores SIGINT and SIGTERM, and the promise resolves', async () => {
    const pm = new ProcessManager();
    const start = Date.now();
    const session = await pm.run({
      command: '/bin/bash',
      args: ['-c', "trap '' INT; trap '' TERM; while :; do :; done"],
      maxExecutionTimeMs: 200,
    });
    const elapsed = Date.now() - start;
    const { snapshot } = await runSession(session);

    // The execution-timeout fired, so timedOut must be true.
    assert.equal(snapshot.timedOut, true);
    // Because both SIGINT and SIGTERM were trapped, only SIGKILL could have
    // terminated the process. This verifies the escalation chain actually
    // runs to completion (i.e. uses processExited, not child.killed).
    assert.equal(
      snapshot.signal,
      'SIGKILL',
      `expected SIGKILL after full escalation, got ${snapshot.signal}`,
    );
    // The full SIGINT -> SIGTERM -> SIGKILL chain takes ~4000ms (2000ms each).
    assert.ok(elapsed >= 3500, `expected full escalation to take >= 3500ms, got ${elapsed}ms`);
  });

  it('cancels immediately when given an already-aborted signal', async () => {
    const pm = new ProcessManager();
    const controller = new AbortController();
    controller.abort();

    const start = Date.now();
    const session = await pm.run({
      command: '/bin/sleep',
      args: ['10'],
      signal: controller.signal,
    });
    const { snapshot } = await runSession(session);
    const elapsed = Date.now() - start;

    // Aborted signals must not be reported as a timeout.
    assert.equal(snapshot.timedOut, false);
    assert.equal(snapshot.truncated, false);
    // The process should be terminated by a kill signal rather than running to completion.
    assert.ok(
      ['SIGINT', 'SIGTERM', 'SIGKILL'].includes(snapshot.signal as string),
      `expected kill signal, got ${snapshot.signal}`,
    );
    // Because the abort handler fires synchronously, we should not have waited
    // anywhere near the full 10s sleep duration.
    assert.ok(elapsed < 5000, `expected immediate cancellation, took ${elapsed}ms`);
  });

  it('truncates when output reaches exactly maxOutputBytes (>= threshold)', async () => {
    const pm = new ProcessManager();
    const session = await pm.run({
      command: '/bin/printf',
      args: ['%.6s', 'abcdef'],
      maxOutputBytes: 6,
    });

    const { snapshot } = await runSession(session);

    assert.equal(snapshot.truncated, true, 'should truncate at exact byte boundary');
    assert.ok(
      snapshot.totalBytes <= 6,
      `expected totalBytes <= 6 with >= threshold, got ${snapshot.totalBytes}`,
    );
  });

  it('emits a started event and yields stdout chunks before finishing', async () => {
    const pm = new ProcessManager();
    const session = await pm.run({
      command: '/bin/echo',
      args: ['streamed'],
    });

    const events: ExecutionEvent[] = [];
    for await (const event of session.stream()) {
      events.push(event);
    }

    const started = events.find((e) => e.type === 'started');
    const stdout = events.filter((e) => e.type === 'stdout');
    const finished = events.find((e) => e.type === 'finished');

    assert.ok(started && started.type === 'started', 'expected a started event');
    assert.ok(
      started.type === 'started' && started.pid > 0,
      'started event must carry a positive pid',
    );
    assert.ok(stdout.length > 0, 'expected at least one stdout event');
    assert.ok(
      stdout.map((e) => (e.type === 'stdout' ? e.chunk : '')).join('').includes('streamed'),
      'stdout payload must include the echo content',
    );
    assert.ok(finished && finished.type === 'finished', 'expected a finished event');
  });
});
