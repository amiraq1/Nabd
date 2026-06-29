import type { AgentEvent } from '../agent/AgentEvents.js';
import type { InferenceEvent } from '../inference/InferenceEvents.js';

export interface EventBase {
  readonly executionId: string;
  readonly traceId?: string;
  readonly sessionId?: string;
  readonly parentExecutionId?: string;
  readonly timestamp: number;
  readonly sequenceNumber: number;
}

export interface SessionQueuedEvent extends EventBase {
  readonly type: 'SessionQueued';
  readonly command: string;
  readonly args: readonly string[];
  readonly cwd: string;
}

export interface SessionStartedEvent extends EventBase {
  readonly type: 'SessionStarted';
  readonly pid: number;
}

export interface StdoutChunkEvent extends EventBase {
  readonly type: 'StdoutChunk';
  readonly chunk: string;
  readonly bytes: number;
}

export interface StderrChunkEvent extends EventBase {
  readonly type: 'StderrChunk';
  readonly chunk: string;
  readonly bytes: number;
}

export interface StdoutBatchEvent extends EventBase {
  readonly type: 'StdoutBatch';
  readonly chunks: readonly string[];
  readonly bytes: number;
}

export interface StderrBatchEvent extends EventBase {
  readonly type: 'StderrBatch';
  readonly chunks: readonly string[];
  readonly bytes: number;
}

export interface ProgressEvent extends EventBase {
  readonly type: 'Progress';
  readonly progress: number;
  readonly total?: number;
}

export interface MetricsEvent extends EventBase {
  readonly type: 'Metrics';
  readonly cpuTime?: number;
  readonly memoryPeak?: number;
}

export interface CancelledEvent extends EventBase {
  readonly type: 'Cancelled';
  readonly reason: string;
}

export interface PolicyViolationEvent extends EventBase {
  readonly type: 'PolicyViolation';
  readonly rule: string;
  readonly message: string;
}

export interface CompletedEvent extends EventBase {
  readonly type: 'Completed';
  readonly exitCode: number | null;
  readonly signal: string | null;
  readonly durationMs: number;
}

export interface FailedEvent extends EventBase {
  readonly type: 'Failed';
  readonly error: string;
  readonly reason: 'timeout' | 'error' | 'truncation';
}

export type ExecutionEventV3 =
  | SessionQueuedEvent
  | SessionStartedEvent
  | StdoutChunkEvent
  | StderrChunkEvent
  | StdoutBatchEvent
  | StderrBatchEvent
  | ProgressEvent
  | MetricsEvent
  | CancelledEvent
  | PolicyViolationEvent
  | CompletedEvent
  | FailedEvent;

export type SystemEvent = ExecutionEventV3 | AgentEvent | InferenceEvent;

export function isExecutionEvent(event: SystemEvent): event is ExecutionEventV3 {
  return 'executionId' in event;
}
