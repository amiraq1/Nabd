export type AgentState =
  | 'INITIALIZING'
  | 'BUILDING_PROMPT'
  | 'WAITING_FOR_LLM'
  | 'VALIDATING_REQUEST'
  | 'EXECUTING'
  | 'OBSERVING'
  | 'DECIDING'
  | 'TERMINATING';

export interface AgentEventBase {
  traceId: string;
  sessionId: string;
  timestamp: number;
}

export interface StateTransitionEvent extends AgentEventBase {
  type: 'StateTransition';
  from: AgentState;
  to: AgentState;
}

export interface PlannerDecisionEvent extends AgentEventBase {
  type: 'PlannerDecision';
  decision: 'CONTINUE' | 'STOP' | 'RETRY_ERROR';
  reason?: string;
}

export interface ToolCallEvent extends AgentEventBase {
  type: 'ToolCall';
  toolName: string;
  arguments: unknown;
  callId: string;
}

export type AgentEvent = StateTransitionEvent | PlannerDecisionEvent | ToolCallEvent;
