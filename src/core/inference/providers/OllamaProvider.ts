import type { InferenceCapabilities, InferenceRequest } from '../InferenceProvider.js';
import type { InferenceEvent } from '../InferenceEvents.js';
import { BaseProvider } from './BaseProvider.js';

export class OllamaProvider extends BaseProvider {
  readonly name = 'Ollama';
  private baseUrl = process.env.OLLAMA_URL || 'http://localhost:11434';
  private model = process.env.OLLAMA_MODEL || 'llama3';

  async health(): Promise<boolean> {
    return this.pool.healthCheck(`${this.baseUrl}/api/version`);
  }

  capabilities(): InferenceCapabilities {
    return {
      supportsStreaming: true,
      supportsVision: false,
      supportsTools: true,
      supportsReasoning: false,
      supportsEmbeddings: true,
      supportsGrammar: false,
      supportsJsonMode: true,
      supportsSystemPrompt: true,
      supportsCancellation: true,
      supportsParallelRequests: false,
    };
  }

  protected buildRequest(request: InferenceRequest) {
    const payload: any = {
      model: this.model,
      prompt: request.prompt,
      stream: true,
    };
    
    if (request.systemPrompt) {
      payload.system = request.systemPrompt;
    }
    
    if (request.jsonMode) {
      payload.format = 'json';
    }

    return {
      url: `${this.baseUrl}/api/generate`,
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(payload)
    };
  }

  protected parseLine(line: string, request: InferenceRequest): InferenceEvent | null {
    if (!line.startsWith('{')) return null;
    const data = JSON.parse(line);
    
    if (data.done) {
      return {
        type: 'Usage',
        promptTokens: data.prompt_eval_count || 0,
        completionTokens: data.eval_count || 0,
        totalTokens: (data.prompt_eval_count || 0) + (data.eval_count || 0),
        traceId: request.traceId,
        sessionId: request.sessionId,
        requestId: request.requestId,
        timestamp: Date.now(),
      };
    }

    return {
      type: 'Token',
      text: data.response || '',
      traceId: request.traceId,
      sessionId: request.sessionId,
      requestId: request.requestId,
      timestamp: Date.now(),
    };
  }
}
