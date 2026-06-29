export interface InferenceEventBase {
  readonly traceId: string;
  readonly sessionId: string;
  readonly requestId: string;
  readonly timestamp: number;
}

export interface InferenceStartedEvent extends InferenceEventBase {
  readonly type: 'InferenceStarted';
  readonly provider: string;
}

export interface TokenEvent extends InferenceEventBase {
  readonly type: 'Token';
  readonly text: string;
}

export interface ReasoningEvent extends InferenceEventBase {
  readonly type: 'Reasoning';
  readonly text: string;
}

export interface ToolCallDetectedEvent extends InferenceEventBase {
  readonly type: 'ToolCallDetected';
  readonly toolName: string;
}

export interface ToolCallFinishedEvent extends InferenceEventBase {
  readonly type: 'ToolCallFinished';
  readonly toolName: string;
  readonly arguments: Record<string, unknown>;
}

export interface UsageEvent extends InferenceEventBase {
  readonly type: 'Usage';
  readonly promptTokens: number;
  readonly completionTokens: number;
  readonly totalTokens: number;
}

export interface InferenceCompletedEvent extends InferenceEventBase {
  readonly type: 'Completed';
  readonly fullText: string;
}

export interface InferenceCancelledEvent extends InferenceEventBase {
  readonly type: 'Cancelled';
}

export interface InferenceFailedEvent extends InferenceEventBase {
  readonly type: 'Failed';
  readonly error: string;
  readonly fatal: boolean;
}

export interface InferenceTimeoutEvent extends InferenceEventBase {
  readonly type: 'Timeout';
  readonly durationMs: number;
}

export type InferenceEvent =
  | InferenceStartedEvent
  | TokenEvent
  | ReasoningEvent
  | ToolCallDetectedEvent
  | ToolCallFinishedEvent
  | UsageEvent
  | InferenceCompletedEvent
  | InferenceCancelledEvent
  | InferenceFailedEvent
  | InferenceTimeoutEvent;
