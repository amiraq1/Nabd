import type { ProcessSessionSnapshot, ProcessSessionStatus, ExecutionMetrics } from './types.js';
import type { EventBus } from './events/EventBus.js';
import { globalEventBus } from './events/EventBus.js';
import { type ExecutionEventV3, type SystemEvent, isExecutionEvent } from './events/ExecutionEvent.js';
import { replayService } from './replay/ReplayService.js';

export interface RegistryStatistics {
  total: number;
  running: number;
  completed: number;
  failed: number;
  maxSessions: number;
}

const DEFAULT_MAX_SESSIONS = 1000;
const ACTIVE_STATUSES = new Set(['queued', 'running']);
const TERMINAL_STATUSES = new Set(['finished', 'cancelled', 'error']);

type Mutable<T> = {
  -readonly [P in keyof T]: T[P] extends object ? Mutable<T[P]> : T[P];
};
type MutableSnapshot = Mutable<ProcessSessionSnapshot>;

export class ExecutionRegistry {
  private readonly maxSessions: number;
  private readonly byId = new Map<string, MutableSnapshot>();
  private readonly byPid = new Map<number, MutableSnapshot>();

  constructor(maxSessions: number = DEFAULT_MAX_SESSIONS, bus = globalEventBus) {
    if (!Number.isFinite(maxSessions) || maxSessions <= 0) {
      throw new Error(`ExecutionRegistry: maxSessions must be a positive number, got ${maxSessions}`);
    }
    this.maxSessions = Math.floor(maxSessions);
    
    bus.subscribe((event) => this.handleEvent(event));
  }

  private handleEvent(event: SystemEvent): void {
    if (!isExecutionEvent(event)) return;

    if (event.type === 'SessionQueued') {
      const snapshot: MutableSnapshot = {
        executionId: event.executionId,
        pid: null,
        command: event.command,
        args: [...event.args],
        cwd: event.cwd,
        status: 'queued',
        startedAt: null,
        endedAt: null,
        exitCode: null,
        signal: null,
        durationMs: null,
        timedOut: false,
        cancelled: false,
        truncated: false,
        stdoutBytes: 0,
        stderrBytes: 0,
        totalBytes: 0,
        metrics: {
          queuedAt: event.timestamp,
          stdoutBytes: 0,
          stderrBytes: 0,
          peakOutputRate: 0,
          terminationReason: 'unknown',
        },
      };
      this.byId.set(event.executionId, snapshot);
      return;
    }

    const snapshot = this.byId.get(event.executionId);
    if (!snapshot) return;

    switch (event.type) {
      case 'SessionStarted':
        if (snapshot.pid !== null && snapshot.pid !== event.pid) {
          const current = this.byPid.get(snapshot.pid);
          if (current === snapshot) {
            this.byPid.delete(snapshot.pid);
          }
        }
        snapshot.pid = event.pid;
        snapshot.status = 'running';
        snapshot.startedAt = event.timestamp;
        snapshot.metrics.startedAt = event.timestamp;
        if (snapshot.metrics.queuedAt !== undefined) {
          snapshot.metrics.waitTime = event.timestamp - snapshot.metrics.queuedAt;
        }
        this.byPid.set(event.pid, snapshot);
        break;
      case 'StdoutChunk':
        snapshot.stdoutBytes += event.bytes;
        snapshot.totalBytes += event.bytes;
        snapshot.metrics.stdoutBytes = snapshot.stdoutBytes;
        break;
      case 'StdoutBatch':
        snapshot.stdoutBytes += event.bytes;
        snapshot.totalBytes += event.bytes;
        snapshot.metrics.stdoutBytes = snapshot.stdoutBytes;
        break;
      case 'StderrChunk':
        snapshot.stderrBytes += event.bytes;
        snapshot.totalBytes += event.bytes;
        snapshot.metrics.stderrBytes = snapshot.stderrBytes;
        break;
      case 'StderrBatch':
        snapshot.stderrBytes += event.bytes;
        snapshot.totalBytes += event.bytes;
        snapshot.metrics.stderrBytes = snapshot.stderrBytes;
        break;
      case 'Metrics':
        // Not all metrics are tracked in the legacy ExecutionMetrics yet
        break;
      case 'Completed':
        snapshot.status = 'finished';
        snapshot.exitCode = event.exitCode;
        snapshot.signal = event.signal;
        snapshot.endedAt = event.timestamp;
        snapshot.durationMs = event.durationMs;
        snapshot.metrics.endedAt = event.timestamp;
        snapshot.metrics.runTime = event.durationMs;
        snapshot.metrics.terminationReason = 'natural';
        this.prune();
        break;
      case 'Cancelled':
        snapshot.status = 'cancelled';
        snapshot.cancelled = true;
        snapshot.signal = event.reason;
        snapshot.endedAt = event.timestamp;
        if (snapshot.startedAt) snapshot.durationMs = event.timestamp - snapshot.startedAt;
        snapshot.metrics.endedAt = event.timestamp;
        snapshot.metrics.runTime = snapshot.durationMs ?? undefined;
        snapshot.metrics.terminationReason = 'cancelled';
        this.prune();
        break;
      case 'Failed':
        snapshot.status = 'error';
        if (event.reason === 'timeout') snapshot.timedOut = true;
        if (event.reason === 'truncation') snapshot.truncated = true;
        snapshot.endedAt = event.timestamp;
        if (snapshot.startedAt) snapshot.durationMs = event.timestamp - snapshot.startedAt;
        snapshot.metrics.endedAt = event.timestamp;
        snapshot.metrics.runTime = snapshot.durationMs ?? undefined;
        snapshot.metrics.terminationReason = event.reason;
        this.prune();
        break;
    }
  }

  getById(executionId: string): ProcessSessionSnapshot | undefined {
    const snap = this.byId.get(executionId);
    return snap ? this.clone(snap) : undefined;
  }

  getByPid(pid: number): ProcessSessionSnapshot | undefined {
    const snap = this.byPid.get(pid);
    return snap ? this.clone(snap) : undefined;
  }

  getRunning(): ProcessSessionSnapshot[] {
    return this.collectByStatus('running');
  }

  getCompleted(): ProcessSessionSnapshot[] {
    return this.collectByStatus('finished');
  }

  getFailed(): ProcessSessionSnapshot[] {
    const out: ProcessSessionSnapshot[] = [];
    for (const snapshot of this.byId.values()) {
      if (snapshot.status === 'error' || snapshot.status === 'cancelled') {
        out.push(this.clone(snapshot));
      }
    }
    return out;
  }

  getHistory(limit?: number): ProcessSessionSnapshot[] {
    const sessions = Array.from(this.byId.values());
    sessions.sort((a, b) => {
      const aQ = a.metrics.queuedAt ?? 0;
      const bQ = b.metrics.queuedAt ?? 0;
      if (aQ !== bQ) return aQ - bQ;
      return a.executionId < b.executionId ? -1 : a.executionId > b.executionId ? 1 : 0;
    });
    const sliced = typeof limit === 'number' && limit >= 0 ? sessions.slice(0, limit) : sessions;
    return sliced.map(s => this.clone(s));
  }

  stats(): RegistryStatistics {
    let running = 0, completed = 0, failed = 0;
    for (const snapshot of this.byId.values()) {
      if (snapshot.status === 'running') running++;
      else if (snapshot.status === 'finished') completed++;
      else if (snapshot.status === 'error' || snapshot.status === 'cancelled') failed++;
    }
    return {
      total: this.byId.size,
      running,
      completed,
      failed,
      maxSessions: this.maxSessions,
    };
  }

  prune(): void {
    const total = this.byId.size;
    if (total <= this.maxSessions) return;
    const toRemove = total - this.maxSessions;
    if (toRemove <= 0) return;

    const terminal: MutableSnapshot[] = [];
    for (const snapshot of this.byId.values()) {
      if (TERMINAL_STATUSES.has(snapshot.status)) {
        terminal.push(snapshot);
      }
    }
    if (terminal.length === 0) return;

    terminal.sort((a, b) => {
      const aEnd = a.endedAt ?? a.metrics.endedAt ?? a.metrics.queuedAt ?? 0;
      const bEnd = b.endedAt ?? b.metrics.endedAt ?? b.metrics.queuedAt ?? 0;
      if (aEnd !== bEnd) return aEnd - bEnd;
      const aQ = a.metrics.queuedAt ?? 0;
      const bQ = b.metrics.queuedAt ?? 0;
      if (aQ !== bQ) return aQ - bQ;
      return a.executionId < b.executionId ? -1 : a.executionId > b.executionId ? 1 : 0;
    });

    const count = Math.min(toRemove, terminal.length);
    for (let i = 0; i < count; i += 1) {
      const snapshot = terminal[i];
      if (!snapshot || ACTIVE_STATUSES.has(snapshot.status)) continue;
      this.byId.delete(snapshot.executionId);
      if (snapshot.pid !== null) {
        const current = this.byPid.get(snapshot.pid);
        if (current === snapshot) {
          this.byPid.delete(snapshot.pid);
        }
      }
    }
  }

  updateMetrics(executionId: string, partialMetrics: Partial<ExecutionMetrics>): void {
    const snapshot = this.byId.get(executionId);
    if (!snapshot) return;
    Object.assign(snapshot.metrics, partialMetrics);
  }

  private collectByStatus(status: ProcessSessionSnapshot['status']): ProcessSessionSnapshot[] {
    const out: ProcessSessionSnapshot[] = [];
    for (const snapshot of this.byId.values()) {
      if (snapshot.status === status) out.push(this.clone(snapshot));
    }
    return out;
  }

  private clone(snapshot: MutableSnapshot): ProcessSessionSnapshot {
    return {
      ...snapshot,
      args: [...snapshot.args],
      metrics: { ...snapshot.metrics }
    };
  }

  formatForReplay(sessionId: string): Record<string, any> | null {
    return replayService.formatForReplay(sessionId, this);
  }

  formatForReplayJSON(sessionId: string): string {
    return replayService.formatForReplayJSON(sessionId, this);
  }

  formatForReplayMarkdown(sessionId: string): string {
    return replayService.formatForReplayMarkdown(sessionId, this);
  }
}

export const executionRegistry = new ExecutionRegistry();
