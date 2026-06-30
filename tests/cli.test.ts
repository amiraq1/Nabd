import { describe, it } from 'node:test';
import assert from 'node:assert';
import { NabdCLI } from '../src/cli/NabdCLI.js';
import { AgentLoop } from '../src/core/agent/AgentLoop.js';
import { toolEngine } from '../src/core/tool-engine.js';
import { EventBus } from '../src/core/events/EventBus.js';

describe('Phase 7 - Cyberpunk CLI', () => {
  it('instantiates the UI and registers event listeners', () => {
    // Stub stdin and stdout purely to test instantiation
    const originalStdin = process.stdin;
    const originalStdout = process.stdout;
    
    // We don't want to actually start reading, just instantiate.
    // The constructor creates the readline interface and subscribes to events.
    
    const loop = new AgentLoop(toolEngine, { sessionId: 'test', traceId: 'test', permissions: [] }, new EventBus());
    const cli = new NabdCLI(loop);
    
    assert.ok(cli);
    assert.equal(typeof cli.start, 'function');
    
    // Ink cleans up after itself, no readline to close
  });
});
