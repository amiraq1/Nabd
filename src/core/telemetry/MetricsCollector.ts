import { globalEventBus } from '../events/EventBus.js';
import { type ExecutionEventV3, type SystemEvent, isExecutionEvent } from '../events/ExecutionEvent.js';
import { executionRegistry } from '../ExecutionRegistry.js';

interface SessionMetricsState {
  firstEventAt: number | null;
  lastEventAt: number | null;
  totalEvents: number;
  stdoutLines: number;
  stderrLines: number;
  totalChunks: number;
  peakChunkSize: number;
}

export class MetricsCollector {
  private states = new Map<string, SessionMetricsState>();

  constructor(
    private readonly bus = globalEventBus,
    private readonly registry = executionRegistry
  ) {
    this.bus.subscribe((event) => this.handleEvent(event));
  }

  private handleEvent(event: SystemEvent): void {
    if (!isExecutionEvent(event)) return;
    
    let state = this.states.get(event.executionId);
    if (!state) {
      state = {
        firstEventAt: event.timestamp,
        lastEventAt: event.timestamp,
        totalEvents: 0,
        stdoutLines: 0,
        stderrLines: 0,
        totalChunks: 0,
        peakChunkSize: 0,
      };
      this.states.set(event.executionId, state);
    }
    
    state.totalEvents += 1;
    state.lastEventAt = event.timestamp;

    if (event.type === 'StdoutChunk') {
      this.processChunkMetrics(state, event.chunk, event.bytes, false);
    } else if (event.type === 'StderrChunk') {
      this.processChunkMetrics(state, event.chunk, event.bytes, true);
    } else if (event.type === 'StdoutBatch') {
      for (const chunk of event.chunks) {
        this.processChunkMetrics(state, chunk, Buffer.byteLength(chunk, 'utf8'), false);
      }
    } else if (event.type === 'StderrBatch') {
      for (const chunk of event.chunks) {
        this.processChunkMetrics(state, chunk, Buffer.byteLength(chunk, 'utf8'), true);
      }
    } else if (['Completed', 'Failed', 'Cancelled'].includes(event.type)) {
      // Calculate and store final metrics
      this.finalizeMetrics(event.executionId, state);
      this.states.delete(event.executionId);
    }
  }

  private processChunkMetrics(state: SessionMetricsState, chunk: string, bytes: number, isStderr: boolean) {
    state.totalChunks += 1;
    if (bytes > state.peakChunkSize) {
      state.peakChunkSize = bytes;
    }
    let lines = 0;
    for (let i = 0; i < chunk.length; i++) {
      if (chunk[i] === '\n') lines++;
    }
    if (isStderr) {
      state.stderrLines += lines;
    } else {
      state.stdoutLines += lines;
    }
  }

  private finalizeMetrics(executionId: string, state: SessionMetricsState) {
    const snapshot = this.registry.getById(executionId);
    if (!snapshot) return;
    
    const m = snapshot.metrics;
    
    const queueWaitMs = (m.startedAt && m.queuedAt) ? m.startedAt - m.queuedAt : 0;
    const executionMs = (m.endedAt && m.startedAt) ? m.endedAt - m.startedAt : 0;
    
    const durationSec = (state.lastEventAt! - state.firstEventAt!) / 1000;
    const eventsPerSecond = durationSec > 0 ? state.totalEvents / durationSec : state.totalEvents;
    const averageChunkSize = state.totalChunks > 0 ? snapshot.totalBytes / state.totalChunks : 0;

    executionRegistry.updateMetrics(executionId, {
      queueWaitMs,
      waitTime: queueWaitMs, // legacy
      executionMs,
      runTime: executionMs, // legacy
      stdoutLines: state.stdoutLines,
      stderrLines: state.stderrLines,
      eventsPerSecond,
      averageChunkSize,
      peakChunkSize: state.peakChunkSize,
    });
  }
}

export const metricsCollector = new MetricsCollector();
