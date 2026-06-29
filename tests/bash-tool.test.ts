// Override the default Termux bash path so tests run on a Linux environment.
process.env.TERMUX_BASH_PATH = '/bin/bash';

import { describe, it, before } from 'node:test';
import assert from 'node:assert';
import { executeBash, executeBashTool } from '../src/core/tools/bash.ts';
import { toolEngine } from '../src/core/tool-engine.ts';
import type { ProcessSession } from '../src/core/ProcessSession.ts';
import type { ExecutionEvent } from '../src/core/types.ts';

type StreamChunk = { chunk: string; isStderr: boolean };

const collectStream = () => {
  const chunks: StreamChunk[] = [];
  return {
    chunks,
    handler: (chunk: string, isStderr: boolean) => {
      chunks.push({ chunk, isStderr });
    },
  };
};

/**
 * Drain a ProcessSession's event stream into typed chunks.
 */
async function drainSession(
  session: ProcessSession,
): Promise<{ chunks: StreamChunk[]; events: ExecutionEvent[] }> {
  const chunks: StreamChunk[] = [];
  const events: ExecutionEvent[] = [];
  for await (const event of session.stream()) {
    events.push(event);
    if (event.type === 'stdout') {
      chunks.push({ chunk: event.chunk, isStderr: false });
    } else if (event.type === 'stderr') {
      chunks.push({ chunk: event.chunk, isStderr: true });
    }
  }
  return { chunks, events };
}

describe('executeBash (legacy)', () => {
  before(() => {
    // Defensive: ensure the env override survives even if another test file
    // mutates process.env during the same node process.
    process.env.TERMUX_BASH_PATH = '/bin/bash';
  });

  it('executes echo and returns a successful result', async () => {
    const { handler } = collectStream();

    const result = await executeBash('echo hello', handler);

    assert.equal(result.exitCode, 0);
    assert.equal(result.signal, null);
    assert.equal(result.timedOut, false);
    assert.equal(result.truncated, false);
  });

  it('propagates the exit code from the bash command', async () => {
    const { handler } = collectStream();

    const result = await executeBash('exit 7', handler);

    assert.equal(result.exitCode, 7);
    assert.equal(result.signal, null);
    assert.equal(result.timedOut, false);
    assert.equal(result.truncated, false);
  });

  it('throws when given an empty command string', async () => {
    const { handler } = collectStream();

    await assert.rejects(
      () => executeBash('', handler),
      /command must be a non-empty string/,
    );
  });

  it('aborts when the external AbortSignal fires', async () => {
    const { handler } = collectStream();
    const abortController = new AbortController();

    const promise = executeBash('sleep 10', handler, abortController.signal);

    setTimeout(() => abortController.abort(), 50);

    const result = await promise;

    // Abort must not be reported as a timeout.
    assert.equal(result.timedOut, false);
    assert.equal(result.truncated, false);
    // Process should have been killed by one of the kill signals.
    assert.ok(
      ['SIGINT', 'SIGTERM', 'SIGKILL'].includes(result.signal as string),
      `expected signal to be SIGINT/SIGTERM/SIGKILL, got ${result.signal}`,
    );
  });
});

describe('executeBashTool (v2)', () => {
  before(() => {
    process.env.TERMUX_BASH_PATH = '/bin/bash';
  });

  it('returns a ProcessSession that emits stdout for echo hello', async () => {
    const session = await executeBashTool.execute(
      { command: 'echo hello' },
      {},
    );

    assert.ok(session, 'expected a ProcessSession');
    assert.equal(typeof session.executionId, 'string');
    assert.equal(session.command, process.env.TERMUX_BASH_PATH);

    const { chunks, events } = await drainSession(session);
    const snapshot = session.snapshot();

    assert.equal(snapshot.status, 'finished');
    assert.equal(snapshot.exitCode, 0);
    assert.equal(snapshot.signal, null);
    assert.equal(snapshot.timedOut, false);
    assert.equal(snapshot.truncated, false);

    const stdout = chunks.filter((c) => !c.isStderr).map((c) => c.chunk).join('');
    assert.match(stdout, /hello\n/);

    const started = events.find((e) => e.type === 'started');
    const finished = events.find((e) => e.type === 'finished');
    assert.ok(started, 'expected a started event');
    assert.ok(finished, 'expected a finished event');
  });

  it('returns a terminal error session for an empty command', async () => {
    const session = await executeBashTool.execute({ command: '' }, {});

    // The session should already be in a terminal error state without
    // requiring us to drain its event stream.
    const snapshot = session.snapshot();
    assert.equal(snapshot.status, 'error');
    const errorEvent = (await (async () => {
      for await (const e of session.stream()) {
        if (e.type === 'error') return e;
      }
      return null;
    })()) as ExecutionEvent | null;
    assert.ok(errorEvent && errorEvent.type === 'error');
  });
});

describe('toolEngine.execute(execute_bash)', () => {
  before(() => {
    process.env.TERMUX_BASH_PATH = '/bin/bash';
  });

  it('returns a ProcessSession that emits stdout for echo hello', async () => {
    // The shared toolEngine singleton does not auto-register tools, so
    // we register executeBashTool here for the test.
    toolEngine.register(executeBashTool);
    const session = await toolEngine.execute(
      'execute_bash',
      { command: 'echo hello' },
      {},
    );

    assert.ok(session, 'expected a ProcessSession');
    assert.equal(typeof session.executionId, 'string');

    const { chunks } = await drainSession(session);
    const snapshot = session.snapshot();

    assert.equal(snapshot.status, 'finished');
    assert.equal(snapshot.exitCode, 0);
    assert.equal(snapshot.signal, null);
    assert.equal(snapshot.timedOut, false);
    assert.equal(snapshot.truncated, false);

    const stdout = chunks.filter((c) => !c.isStderr).map((c) => c.chunk).join('');
    assert.match(stdout, /hello\n/);
  });

  it('throws when the tool name is unknown', async () => {
    await assert.rejects(
      () => toolEngine.execute('not_a_real_tool', {}, {}),
      /not registered/,
    );
  });
});
