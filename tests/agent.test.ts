import { describe, it } from 'node:test';
import assert from 'node:assert';
import { CircuitBreaker } from '../src/core/agent/CircuitBreaker.ts';
import { Planner } from '../src/core/agent/Planner.ts';
import { LLMProtocol } from '../src/core/agent/LLMProtocol.ts';
import { AgentLoop } from '../src/core/agent/AgentLoop.ts';
import { EventBus } from '../src/core/events/EventBus.ts';

describe('Phase 5 - Agent Capabilities', () => {
  describe('CircuitBreaker', () => {
    it('opens after threshold failures and becomes half-open', () => {
      const cb = new CircuitBreaker({ failureThreshold: 2, resetTimeoutMs: 50 });
      assert.equal(cb.getState(), 'Closed');
      cb.recordFailure();
      assert.equal(cb.getState(), 'Closed');
      cb.recordFailure();
      assert.equal(cb.getState(), 'Open');
      
      // simulate time travel for half-open (we can just override nextAttemptAt or wait)
      const now = Date.now();
      while(Date.now() - now < 55) {} // busy wait for 55ms
      assert.equal(cb.getState(), 'HalfOpen');
    });
  });

  describe('Planner', () => {
    it('retries on malformed output and respects circuit breaker', () => {
      const planner = new Planner({ maxIterations: 5 });
      
      const d1 = planner.decide('just some text');
      assert.equal(d1.action, 'RETRY_ERROR');

      const d2 = planner.decide('more text');
      assert.equal(d2.action, 'RETRY_ERROR');

      const d3 = planner.decide('even more text');
      assert.equal(d3.action, 'RETRY_ERROR');

      const d4 = planner.decide('fourth text');
      assert.equal(d4.action, 'STOP'); // circuit breaker is now open
    });

    it('stops when max iterations reached', () => {
      const planner = new Planner({ maxIterations: 2 });
      planner.decide('{"tool":"t","arguments":{}}');
      planner.recordToolSuccess();
      planner.decide('{"tool":"t","arguments":{}}');
      planner.recordToolSuccess();
      const d3 = planner.decide('{"tool":"t","arguments":{}}');
      assert.equal(d3.action, 'STOP');
      assert.match((d3 as any).reason, /Max iterations/);
    });
  });

  describe('LLMProtocol', () => {
    it('parses strict JSON', () => {
      const res = LLMProtocol.parseToolCall('{"tool":"t1","arguments":{"a":1}}');
      assert.deepEqual(res, { tool: 't1', arguments: { a: 1 } });
    });

    it('parses partial JSON block', () => {
      const res = LLMProtocol.parseToolCall('Here is my call: {"tool":"t2","arguments":{}} and more text.');
      assert.deepEqual(res, { tool: 't2', arguments: {} });
    });

    it('parses using regex fallback for malformed JSON', () => {
      const res = LLMProtocol.parseToolCall('tool: "t3", arguments: { "b": 2 }');
      assert.deepEqual(res, { tool: 't3', arguments: { b: 2 } });
    });

    it('never crashes on garbage', () => {
      const res = LLMProtocol.parseToolCall('tool: "t4", arguments: { oops ]');
      assert.deepEqual(res, { tool: 't4', arguments: {} }); // fallback to empty args
      const res2 = LLMProtocol.parseToolCall('{ "tool" ');
      assert.equal(res2, null);
    });
  });

  describe('AgentLoop Checkpointing', () => {
    it('restores state from checkpoint', () => {
      const loop = new AgentLoop({} as any, { sessionId: 'test' } as any, new EventBus());
      assert.equal(loop.getState(), 'INITIALIZING');
      
      loop.restoreCheckpoint({ state: 'OBSERVING', iterations: 5, traceId: 'test-trace' });
      assert.equal(loop.getState(), 'OBSERVING');
      assert.equal((loop as any).planner.getIterations(), 5);
      assert.equal((loop as any).traceId, 'test-trace');
    });
  });
});
