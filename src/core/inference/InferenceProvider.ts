import type { InferenceEvent } from './InferenceEvents.js';

export interface InferenceCapabilities {
  supportsStreaming: boolean;
  supportsVision: boolean;
  supportsTools: boolean;
  supportsReasoning: boolean;
  supportsEmbeddings: boolean;
  supportsGrammar: boolean;
  supportsJsonMode: boolean;
  supportsSystemPrompt: boolean;
  supportsCancellation: boolean;
  supportsParallelRequests: boolean;
}

export interface InferenceRequest {
  traceId: string;
  sessionId: string;
  requestId: string;
  prompt: string;
  systemPrompt?: string;
  jsonMode?: boolean;
  tools?: any[]; // Simplified for this phase, typically a schema list
  signal?: AbortSignal;
}

export interface InferenceProvider {
  /** Name of the provider (e.g. "Ollama", "OpenAI") */
  readonly name: string;
  
  initialize(): Promise<void>;
  shutdown(): Promise<void>;
  generate(request: InferenceRequest): AsyncGenerator<InferenceEvent, void, unknown>;
  cancel(requestId: string): void;
  health(): Promise<boolean>;
  capabilities(): InferenceCapabilities;
}
