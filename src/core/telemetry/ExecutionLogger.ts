import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import { EventBus, globalEventBus } from '../events/EventBus.js';
import { type ExecutionEventV3, type SystemEvent, isExecutionEvent } from '../events/ExecutionEvent.js';
import { ExecutionRegistry, executionRegistry } from '../ExecutionRegistry.js';

interface LoggerState {
  stdoutHash: crypto.Hash;
  stderrHash: crypto.Hash;
}

export class ExecutionLogger {
  private readonly stream: fs.WriteStream;
  private readonly states = new Map<string, LoggerState>();

  constructor(
    private readonly logFilePath: string,
    private readonly bus = globalEventBus,
    private readonly registry = executionRegistry
  ) {
    const dir = path.dirname(logFilePath);
    if (!fs.existsSync(dir)) {
      fs.mkdirSync(dir, { recursive: true });
    }
    this.stream = fs.createWriteStream(this.logFilePath, { flags: 'a', encoding: 'utf8' });
    this.bus.subscribe((event) => this.handleEvent(event));
    
    // Attempt graceful shutdown
    const onShutdown = () => {
      this.stream.end();
    };
    process.on('beforeExit', onShutdown);
    process.on('SIGINT', onShutdown);
    process.on('SIGTERM', onShutdown);
  }

  private handleEvent(event: SystemEvent): void {
    if (!isExecutionEvent(event)) return;
    
    if (event.type === 'SessionQueued') {
      this.states.set(event.executionId, {
        stdoutHash: crypto.createHash('sha256'),
        stderrHash: crypto.createHash('sha256'),
      });
      return;
    }

    const state = this.states.get(event.executionId);
    if (!state) return;

    if (event.type === 'StdoutChunk') {
      state.stdoutHash.update(event.chunk);
    } else if (event.type === 'StderrChunk') {
      state.stderrHash.update(event.chunk);
    } else if (event.type === 'StdoutBatch') {
      for (const chunk of event.chunks) {
        state.stdoutHash.update(chunk);
      }
    } else if (event.type === 'StderrBatch') {
      for (const chunk of event.chunks) {
        state.stderrHash.update(chunk);
      }
    } else if (['Completed', 'Failed', 'Cancelled'].includes(event.type)) {
      this.writeTerminalLog(event);
      this.states.delete(event.executionId);
    }
  }

  private writeTerminalLog(event: ExecutionEventV3) {
    const state = this.states.get(event.executionId);
    const snapshot = this.registry.getById(event.executionId);
    if (!state || !snapshot) return;

    let exitCode = snapshot.exitCode;
    let signal = snapshot.signal;
    let durationMs = snapshot.durationMs ?? 0;
    
    const record = {
      sessionId: event.executionId,
      sequence: event.sequenceNumber,
      timestamp: event.timestamp,
      command: snapshot.command,
      exitCode,
      durationMs,
      status: snapshot.status,
      signal,
      outputBytes: snapshot.totalBytes,
      stdoutBytes: snapshot.stdoutBytes,
      stderrBytes: snapshot.stderrBytes,
      policyId: 'default', // placeholder, policies not strictly IDed yet
      queueWaitMs: snapshot.metrics.queueWaitMs ?? 0,
      executionTimeMs: snapshot.metrics.executionMs ?? 0,
      stdoutHash: state.stdoutHash.digest('hex'),
      stderrHash: state.stderrHash.digest('hex'),
    };

    // Fast, append-only write
    this.stream.write(JSON.stringify(record) + '\n');
  }
}

export const executionLogger = new ExecutionLogger(path.join(process.cwd(), 'logs', 'executions.jsonl'));
