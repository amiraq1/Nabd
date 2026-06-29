/**
 * Shared types for the tool execution system.
 *
 * These describe the contract between the ToolEngine, individual tool
 * implementations, and any callers (e.g. an agent loop) that invoke tools
 * and stream their output.
 */

import type { ProcessSession } from './ProcessSession.js';

/**
 * Callback invoked for each chunk of output produced by a running tool.
 *
 * Implementations should be tolerant of partial UTF-8 sequences split across
 * chunks. The `isStderr` flag indicates whether the chunk came from the
 * process's standard error stream rather than standard output.
 *
 * @param chunk  - Raw text chunk emitted by the tool's process.
 * @param isStderr - `true` when the chunk originates from stderr.
 */
export type ToolStreamHandler = (chunk: string, isStderr: boolean) => void;

/**
 * Result of executing a single tool invocation.
 */
export interface ToolExecutionResult {
  /** Stable identifier correlating this result with the original invocation. */
  executionId: string;
  /** Process exit code, or `null` if the process was terminated by a signal. */
  exitCode: number | null;
  /** Signal that terminated the process, or `null` if it exited normally. */
  signal: string | null;
  /** Wall-clock duration of the execution in milliseconds. */
  durationMs: number;
  /** `true` when execution was aborted because the timeout was reached. */
  timedOut: boolean;
  /** `true` when captured output exceeded the configured byte limit. */
  truncated: boolean;
  /** Total number of output bytes captured (stdout + stderr). */
  outputByteCount: number;
}

/**
 * Options controlling how a single tool process is spawned and supervised.
 */
export interface ProcessOptions {
  /** Executable to invoke. */
  command: string;
  /** Positional arguments passed to the executable. */
  args: string[];
  /** Working directory for the child process. */
  cwd?: string;
  /** Environment variables for the child process. */
  env?: Record<string, string | undefined>;
  /** Maximum wall-clock execution time before the process is killed. */
  maxExecutionTimeMs?: number;
  /** Maximum number of output bytes to retain before truncating. */
  maxOutputBytes?: number;
  /** External abort signal forwarded to the child process. */
  signal?: AbortSignal;
  /** Streaming callback invoked as the process produces output. */
  onStream?: ToolStreamHandler;
}

/**
 * Runtime context passed to a tool's `execute` function.
 *
 * Carries request-scoped concerns (streaming callback, abort signal) that are
 * orthogonal to the tool's own arguments.
 */
export interface ToolContext {
  /** Optional streaming callback for incremental output. */
  onStream?: ToolStreamHandler;
  /** Optional abort signal to cancel execution cooperatively. */
  signal?: AbortSignal;
}

/**
 * Definition of a single tool registered with the ToolEngine.
 */
export interface ToolDefinition {
  /** Unique tool name used for dispatch. */
  name: string;
  /** Human-readable description of what the tool does. */
  description: string;
  /**
   * JSON Schema describing the tool's argument shape. Consumers (e.g. an LLM
   * client) use this to validate and prompt for arguments.
   */
  parameters: Record<string, unknown>;
  /**
   * Returns the execution policy overrides for this invocation. The
   * ToolEngine merges this with its default policy via
   * `PolicyEngine.mergePolicy` before validation.
   */
  getPolicy?: (args: unknown) => Partial<ExecutionPolicy>;
  /**
   * Executes the tool with the supplied (already-validated) arguments and
   * returns a `ProcessSession` representing the running execution.
   */
  execute: (args: unknown, context: ToolContext) => Promise<ProcessSession>;
}

/* ---------------------------------------------------------------------------
 * Execution Runtime v2 — ProcessSession, ExecutionPolicy, ExecutionEvent
 * ---------------------------------------------------------------------------
 * These types describe the runtime entity that owns a single execution and the
 * events that flow from it. They are kept separate from the legacy
 * ToolExecutionResult types above to maintain backward compatibility with the
 * v1 callers.
 */

/** Lifecycle status of a single execution managed by ProcessSession. */
export type ProcessSessionStatus =
  | 'queued'
  | 'running'
  | 'finished'
  | 'cancelled'
  | 'error';

/** Timing and throughput metrics captured for a single execution. */
export interface ExecutionMetrics {
  /** Wall-clock timestamp when the session was created and queued. */
  queuedAt?: number;
  /** Wall-clock timestamp when the child process was spawned. */
  startedAt?: number;
  /** Wall-clock timestamp when the session reached a terminal state. */
  endedAt?: number;
  /** Time spent queued (startedAt - queuedAt), in milliseconds. */
  waitTime?: number;
  /** Time spent running (endedAt - startedAt), in milliseconds. */
  runTime?: number;
  /** Number of stdout bytes emitted by the child process. */
  stdoutBytes: number;
  /** Number of stderr bytes emitted by the child process. */
  stderrBytes: number;
  /** Peak throughput observed, in bytes/second, over 1-second windows. */
  peakOutputRate: number;
  /** Reason the execution reached a terminal state. */
  terminationReason:
    | 'natural'
    | 'timeout'
    | 'cancelled'
    | 'truncation'
    | 'error'
    | 'unknown';
}

/** Declarative policy applied to a single execution. */
export interface ExecutionPolicy {
  /** Maximum wall-clock execution time before the process is killed. */
  maxExecutionTimeMs: number;
  /** Maximum output bytes (stdout + stderr) to retain before truncation. */
  maxOutputBytes: number;
  /** Whether the tool is permitted to access the network. */
  allowNetwork: boolean;
  /** Whether the tool is permitted to write to the filesystem. */
  allowFilesystemWrite: boolean;
  /** Whether the tool is permitted to delete files. */
  allowDelete: boolean;
  /** Whether the tool may leave background processes behind. */
  allowBackgroundProcess: boolean;
  /** Working directory for the child process. */
  workingDirectory: string;
  /** Environment variables passed to the child process. */
  environment: Record<string, string | undefined>;
  /** Commands the tool is permitted to invoke. */
  allowedCommands: string[];
}

/** Structured description of a single policy violation. */
export interface PolicyViolation {
  rule: keyof ExecutionPolicy | string;
  message: string;
}

/** Event emitted when the child process writes to stdout. */
export interface StdoutEvent {
  type: 'stdout';
  executionId: string;
  chunk: string;
  bytes: number;
}

/** Event emitted when the child process writes to stderr. */
export interface StderrEvent {
  type: 'stderr';
  executionId: string;
  chunk: string;
  bytes: number;
}

/** Event emitted when the child process has been spawned. */
export interface StartedEvent {
  type: 'started';
  executionId: string;
  pid: number;
  startedAt: number;
}

/** Event emitted when the child process exits normally or via signal. */
export interface FinishedEvent {
  type: 'finished';
  executionId: string;
  exitCode: number | null;
  signal: string | null;
  durationMs: number;
  totalBytes: number;
}

/** Event emitted when execution is cancelled by the caller. */
export interface CancelledEvent {
  type: 'cancelled';
  executionId: string;
  signal: string | null;
  durationMs: number;
}

/** Event emitted when execution exceeds the configured time limit. */
export interface TimeoutEvent {
  type: 'timeout';
  executionId: string;
  signal: string | null;
  durationMs: number;
}

/** Event emitted when execution fails due to an internal error. */
export interface ErrorEvent {
  type: 'error';
  executionId: string;
  error: string;
}

/** Discriminated union of every event emitted by ProcessSession. */
export type ExecutionEvent =
  | StdoutEvent
  | StderrEvent
  | StartedEvent
  | FinishedEvent
  | CancelledEvent
  | TimeoutEvent
  | ErrorEvent;

/** Immutable snapshot of a ProcessSession's state at a point in time. */
export interface ProcessSessionSnapshot {
  executionId: string;
  pid: number | null;
  command: string;
  args: string[];
  cwd: string;
  status: ProcessSessionStatus;
  startedAt: number | null;
  endedAt: number | null;
  exitCode: number | null;
  signal: string | null;
  durationMs: number | null;
  timedOut: boolean;
  cancelled: boolean;
  truncated: boolean;
  stdoutBytes: number;
  stderrBytes: number;
  totalBytes: number;
  metrics: ExecutionMetrics;
}
