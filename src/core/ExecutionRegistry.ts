import type { ProcessSession } from './ProcessSession.js';
import type { ProcessSessionSnapshot } from './types.js';

/**
 * Aggregate counts of sessions currently tracked by the registry.
 */
export interface RegistryStatistics {
  /** Total number of registered sessions (running + terminal). */
  total: number;
  /** Number of sessions whose status is `'running'`. */
  running: number;
  /** Number of sessions whose status is `'finished'`. */
  completed: number;
  /** Number of sessions whose status is `'error'` or `'cancelled'`. */
  failed: number;
  /** Configured upper bound on registered sessions before pruning kicks in. */
  maxSessions: number;
}

/** Default upper bound used when no `maxSessions` is supplied to the ctor. */
const DEFAULT_MAX_SESSIONS = 1000;

/**
 * Statuses that represent a session that has not yet reached a terminal
 * state and must therefore never be pruned.
 */
const ACTIVE_STATUSES = new Set(['queued', 'running']);

/**
 * Statuses that represent a finished execution and are eligible for
 * pruning once `maxSessions` is exceeded.
 */
const TERMINAL_STATUSES = new Set(['finished', 'cancelled', 'error']);

/**
 * Central lookup of every `ProcessSession` known to the runtime.
 *
 * The registry maintains two indices — by `executionId` and by `pid` — and
 * produces immutable snapshots for consumers. It also enforces a soft cap
 * on the number of tracked sessions by pruning the oldest terminal entries
 * once the limit is exceeded.
 */
export class ExecutionRegistry {
  private readonly maxSessions: number;
  private readonly byId = new Map<string, ProcessSession>();
  private readonly byPid = new Map<number, ProcessSession>();

  constructor(maxSessions: number = DEFAULT_MAX_SESSIONS) {
    if (!Number.isFinite(maxSessions) || maxSessions <= 0) {
      throw new Error(
        `ExecutionRegistry: maxSessions must be a positive number, got ${maxSessions}`,
      );
    }
    this.maxSessions = Math.floor(maxSessions);
  }

  /**
   * Register a session. Indexes it by `executionId` immediately and, if the
   * session already has a `pid`, also by `pid`.
   *
   * Re-registering an `executionId` overwrites the previous entry.
   */
  register(session: ProcessSession): void {
    this.byId.set(session.executionId, session);
    if (session.pid !== null) {
      this.byPid.set(session.pid, session);
    }
  }

  /**
   * Remove a session from both indices. Safe to call multiple times.
   */
  unregister(session: ProcessSession): void {
    this.byId.delete(session.executionId);
    if (session.pid !== null) {
      const current = this.byPid.get(session.pid);
      if (current === session) {
        this.byPid.delete(session.pid);
      }
    }
  }

  /**
   * Update the PID a session is indexed under.
   *
   * Called by the `ProcessManager` once the child process has been spawned
   * and `session.pid` becomes known. Sets `session.pid` to the supplied
   * value and refreshes the `byPid` index — removing the previous mapping,
   * if any, before inserting the new one.
   */
  updatePid(session: ProcessSession, pid: number): void {
    if (!Number.isInteger(pid) || pid <= 0) {
      throw new Error(
        `ExecutionRegistry.updatePid: pid must be a positive integer, got ${pid}`,
      );
    }
    if (session.pid !== null && session.pid !== pid) {
      const current = this.byPid.get(session.pid);
      if (current === session) {
        this.byPid.delete(session.pid);
      }
    }
    session.pid = pid;
    this.byPid.set(pid, session);
  }

  /** Look up a session by its `executionId`. */
  getById(executionId: string): ProcessSession | undefined {
    return this.byId.get(executionId);
  }

  /** Look up a session by its OS process id. */
  getByPid(pid: number): ProcessSession | undefined {
    return this.byPid.get(pid);
  }

  /** Snapshot every session whose status is `'running'`. */
  getRunning(): ProcessSessionSnapshot[] {
    return this.collectByStatus('running');
  }

  /** Snapshot every session whose status is `'finished'`. */
  getCompleted(): ProcessSessionSnapshot[] {
    return this.collectByStatus('finished');
  }

  /**
   * Snapshot every session that reached a failure terminal state
   * (status `'error'` or `'cancelled'`).
   */
  getFailed(): ProcessSessionSnapshot[] {
    const out: ProcessSessionSnapshot[] = [];
    for (const session of this.byId.values()) {
      if (session.status === 'error' || session.status === 'cancelled') {
        out.push(session.snapshot());
      }
    }
    return out;
  }

  /**
   * Return snapshots of every registered session, ordered oldest first by
   * `queuedAt` (with insertion order as a stable tiebreaker). If `limit` is
   * supplied, only the first `limit` entries are returned.
   */
  getHistory(limit?: number): ProcessSessionSnapshot[] {
    const sessions = Array.from(this.byId.values());
    sessions.sort((a, b) => {
      const aQ = a.metrics.queuedAt ?? 0;
      const bQ = b.metrics.queuedAt ?? 0;
      if (aQ !== bQ) {
        return aQ - bQ;
      }
      // Fallback to insertion order via executionId to guarantee stability.
      return a.executionId < b.executionId ? -1 : a.executionId > b.executionId ? 1 : 0;
    });
    const sliced =
      typeof limit === 'number' && limit >= 0
        ? sessions.slice(0, limit)
        : sessions;
    return sliced.map((session) => session.snapshot());
  }

  /** Return aggregate counts of tracked sessions. */
  stats(): RegistryStatistics {
    let running = 0;
    let completed = 0;
    let failed = 0;
    for (const session of this.byId.values()) {
      switch (session.status) {
        case 'running':
          running += 1;
          break;
        case 'finished':
          completed += 1;
          break;
        case 'error':
        case 'cancelled':
          failed += 1;
          break;
        case 'queued':
          break;
      }
    }
    return {
      total: this.byId.size,
      running,
      completed,
      failed,
      maxSessions: this.maxSessions,
    };
  }

  /**
   * If the total number of registered sessions exceeds `maxSessions`,
   * remove the oldest terminal sessions until the total is within bounds.
   *
   * Sessions whose status is `'queued'` or `'running'` are never pruned; if
   * no terminal sessions remain and the cap is still exceeded, this method
   * is a no-op.
   */
  prune(): void {
    const total = this.byId.size;
    if (total <= this.maxSessions) {
      return;
    }
    const toRemove = total - this.maxSessions;
    if (toRemove <= 0) {
      return;
    }

    const terminal: ProcessSession[] = [];
    for (const session of this.byId.values()) {
      if (TERMINAL_STATUSES.has(session.status)) {
        terminal.push(session);
      }
    }
    if (terminal.length === 0) {
      return;
    }

    terminal.sort((a, b) => {
      const aEnd = a.endedAt ?? a.metrics.endedAt ?? a.metrics.queuedAt ?? 0;
      const bEnd = b.endedAt ?? b.metrics.endedAt ?? b.metrics.queuedAt ?? 0;
      if (aEnd !== bEnd) {
        return aEnd - bEnd;
      }
      const aQ = a.metrics.queuedAt ?? 0;
      const bQ = b.metrics.queuedAt ?? 0;
      if (aQ !== bQ) {
        return aQ - bQ;
      }
      return a.executionId < b.executionId ? -1 : a.executionId > b.executionId ? 1 : 0;
    });

    const count = Math.min(toRemove, terminal.length);
    for (let i = 0; i < count; i += 1) {
      const session = terminal[i];
      if (session === undefined) {
        break;
      }
      // Re-check status in case it transitioned between collection and prune.
      if (ACTIVE_STATUSES.has(session.status)) {
        continue;
      }
      this.unregister(session);
    }
  }

  private collectByStatus(
    status: ProcessSessionSnapshot['status'],
  ): ProcessSessionSnapshot[] {
    const out: ProcessSessionSnapshot[] = [];
    for (const session of this.byId.values()) {
      if (session.status === status) {
        out.push(session.snapshot());
      }
    }
    return out;
  }
}

/**
 * Singleton registry allowed by the architecture constraints.
 */
export const executionRegistry = new ExecutionRegistry();
