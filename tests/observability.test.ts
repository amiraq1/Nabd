import { describe, it, before, after } from 'node:test';
import assert from 'node:assert';
import fs from 'node:fs';
import path from 'node:path';
import { globalEventBus, EventBus } from '../src/core/events/EventBus.ts';
import { ProcessSession } from '../src/core/ProcessSession.ts';
import { ExecutionLogger } from '../src/core/telemetry/ExecutionLogger.ts';
import { ReplayService } from '../src/core/replay/ReplayService.ts';
import { MetricsCollector } from '../src/core/telemetry/MetricsCollector.ts';
import { truncateOutput } from '../src/core/replay/AdaptiveTruncator.ts';
import { RuntimeInvariantError } from '../src/core/errors.ts';
import { executionRegistry, ExecutionRegistry } from '../src/core/ExecutionRegistry.ts';

describe('Phase 3 - Observability & Forensics', () => {

  describe('AdaptiveTruncator', () => {
    it('keeps <= 40 lines intact', () => {
      const input = Array.from({ length: 30 }, (_, i) => `line ${i}`).join('\n');
      assert.equal(truncateOutput(input), input);
    });

    it('truncates > 40 lines correctly', () => {
      const input = Array.from({ length: 500 }, (_, i) => `line ${i}`).join('\n');
      const output = truncateOutput(input);
      const lines = output.split('\n');
      assert.ok(lines.includes('line 0'));
      assert.ok(lines.includes('line 19'));
      assert.ok(lines.includes('... 460 lines omitted ...'));
      assert.ok(lines.includes('line 480'));
      assert.ok(lines.includes('line 499'));
      assert.ok(!lines.includes('line 100'));
    });
  });

  describe('ProcessSession invariants & immutability', () => {
    it('throws RuntimeInvariantError if events are emitted out of order', () => {
      const session = new ProcessSession({ executionId: 'test-inv', command: 'echo', args: [], cwd: '/' });
      session.start(123);
      session.finish(0, null);
      
      // Attempting to append stdout after finish
      assert.throws(() => {
        session.appendStdout('hello');
      }, RuntimeInvariantError);
    });

    it('emits immutable events', async () => {
      const session = new ProcessSession({ executionId: 'test-freeze', command: 'echo', args: [], cwd: '/' });
      let captured: any;
      const onEvent = (e: any) => { if (e.executionId === 'test-freeze') captured = e; };
      globalEventBus.subscribe(onEvent);
      
      session.start(123);
      assert.ok(Object.isFrozen(captured));
      assert.throws(() => {
        captured.pid = 999;
      });
    });

    it('compresses consecutive chunks into StdoutBatch on flush', async () => {
      const session = new ProcessSession({ executionId: 'test-batch', command: 'echo', args: [], cwd: '/' });
      let batches = 0;
      const onEvent = (e: any) => {
        if (e.executionId === 'test-batch' && e.type === 'StdoutBatch') batches++;
      };
      globalEventBus.subscribe(onEvent);
      
      session.start(123);
      session.appendStdout('chunk1');
      session.appendStdout('chunk2');
      
      // Wait for setImmediate to flush
      await new Promise(r => setImmediate(r));
      assert.equal(batches, 1);
    });
  });

  describe('ExecutionLogger', () => {
    const logPath = path.join(process.cwd(), 'logs', 'test.jsonl');

    before(() => {
      if (fs.existsSync(logPath)) fs.unlinkSync(logPath);
    });

    after(() => {
      if (fs.existsSync(logPath)) fs.unlinkSync(logPath);
    });

    it('writes valid JSONL only on terminal events', async () => {
      const localBus = new EventBus();
      const localRegistry = new ExecutionRegistry(100, localBus);
      const logger = new ExecutionLogger(logPath, localBus, localRegistry);
      const metrics = new MetricsCollector(localBus, localRegistry);
      
      // Manually simulate execution
      const ts = Date.now();
      localBus.emit({ type: 'SessionQueued', executionId: 'log-1', timestamp: ts, sequenceNumber: 1, command: 'ls', args: [], cwd: '/' });
      localBus.emit({ type: 'SessionStarted', executionId: 'log-1', timestamp: ts, sequenceNumber: 2, pid: 1 });
      localBus.emit({ type: 'StdoutChunk', executionId: 'log-1', timestamp: ts, sequenceNumber: 3, chunk: 'hello', bytes: 5 });
      localBus.emit({ type: 'Completed', executionId: 'log-1', timestamp: ts, sequenceNumber: 4, exitCode: 0, signal: null, durationMs: 10 });
      
      await new Promise(r => setTimeout(r, 50)); // let stream write
      
      const content = fs.readFileSync(logPath, 'utf8').trim();
      const lines = content.split('\n');
      assert.equal(lines.length, 1); // Only terminal
      
      const record = JSON.parse(lines[0]);
      assert.equal(record.sessionId, 'log-1');
      assert.equal(record.exitCode, 0);
      assert.ok(record.stdoutHash);
    });
  });

  describe('ReplayService', () => {
    it('reconstructs timeline and output', async () => {
      const localBus = new EventBus();
      const localRegistry = new ExecutionRegistry(100, localBus);
      const replay = new ReplayService(localBus);

      const ts = Date.now();
      localBus.emit({ type: 'SessionQueued', executionId: 'rep-1', timestamp: ts, sequenceNumber: 1, command: 'cat', args: [], cwd: '/' });
      localBus.emit({ type: 'SessionStarted', executionId: 'rep-1', timestamp: ts + 10, sequenceNumber: 2, pid: 1 });
      localBus.emit({ type: 'StdoutChunk', executionId: 'rep-1', timestamp: ts + 20, sequenceNumber: 3, chunk: 'line1\n', bytes: 6 });
      localBus.emit({ type: 'Completed', executionId: 'rep-1', timestamp: ts + 30, sequenceNumber: 4, exitCode: 0, signal: null, durationMs: 20 });

      const json = JSON.parse(replay.formatForReplayJSON('rep-1', localRegistry));
      assert.equal(json.Session, 'rep-1');
      assert.equal(json.Command, 'cat');
      assert.equal(json.Timeline.length, 4); // Queued, Started, Running (synthetic), Completed
      assert.equal(json.Timeline[0].state, 'Queued');
      assert.equal(json.Timeline[1].state, 'Started');
      assert.equal(json.Timeline[2].state, 'Running');
      assert.equal(json.Timeline[3].state, 'Completed');
      assert.equal(json["Captured output"].stdout, 'line1\n');
    });

    it('survives 100MB stdout', async () => {
      const localBus = new EventBus();
      const localRegistry = new ExecutionRegistry(100, localBus);
      const replay = new ReplayService(localBus);

      const ts = Date.now();
      localBus.emit({ type: 'SessionQueued', executionId: 'rep-big', timestamp: ts, sequenceNumber: 1, command: 'yes', args: [], cwd: '/' });
      localBus.emit({ type: 'SessionStarted', executionId: 'rep-big', timestamp: ts + 10, sequenceNumber: 2, pid: 1 });
      
      // Simulate 100MB via large chunks
      const largeChunk = 'y\n'.repeat(50000); // 100KB chunk
      for (let i = 0; i < 1000; i++) { // 1000 * 100KB = 100MB
        localBus.emit({ type: 'StdoutChunk', executionId: 'rep-big', timestamp: ts + 20, sequenceNumber: 3 + i, chunk: largeChunk, bytes: 100000 });
      }

      localBus.emit({ type: 'Completed', executionId: 'rep-big', timestamp: ts + 30, sequenceNumber: 2000, exitCode: 0, signal: null, durationMs: 20 });

      const json = JSON.parse(replay.formatForReplayJSON('rep-big', localRegistry));
      assert.ok(json["Captured output"].stdout.length < 1000000); // must be truncated heavily
    });
  });
});
