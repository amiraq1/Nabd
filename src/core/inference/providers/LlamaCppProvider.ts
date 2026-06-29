import type { InferenceCapabilities, InferenceRequest } from '../InferenceProvider.js';
import type { InferenceEvent } from '../InferenceEvents.js';
import { BaseProvider } from './BaseProvider.js';

export class LlamaCppProvider extends BaseProvider {
  readonly name = 'LlamaCpp';
  private baseUrl = process.env.LLAMACPP_URL || 'http://localhost:8080';

  async health(): Promise<boolean> {
    return this.pool.healthCheck(`${this.baseUrl}/health`);
  }

  capabilities(): InferenceCapabilities {
    return {
      supportsStreaming: true,
      supportsVision: false,
      supportsTools: false,
      supportsReasoning: false,
      supportsEmbeddings: false,
      supportsGrammar: true,
      supportsJsonMode: true,
      supportsSystemPrompt: true,
      supportsCancellation: true,
      supportsParallelRequests: false,
    };
  }

  protected buildRequest(request: InferenceRequest) {
    let prompt = request.prompt;
    if (request.systemPrompt) {
      prompt = `[SYSTEM]\n${request.systemPrompt}\n[/SYSTEM]\n` + prompt;
    }

    const payload: any = {
      prompt,
      stream: true,
    };

    if (request.jsonMode) {
      payload.json_schema = {}; // Minimal representation of JSON mode requirement
    }

    return {
      url: `${this.baseUrl}/completion`,
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(payload)
    };
  }

  protected parseLine(line: string, request: InferenceRequest): InferenceEvent | null {
    if (!line.startsWith('data: ')) return null;
    const dataStr = line.slice(6).trim();
    if (dataStr === '[DONE]') return null;

    const data = JSON.parse(dataStr);
    
    if (data.stop) {
      return {
        type: 'Usage',
        promptTokens: data.tokens_evaluated || 0,
        completionTokens: data.tokens_predicted || 0,
        totalTokens: (data.tokens_evaluated || 0) + (data.tokens_predicted || 0),
        traceId: request.traceId,
        sessionId: request.sessionId,
        requestId: request.requestId,
        timestamp: Date.now(),
      };
    }

    if (data.content) {
      return {
        type: 'Token',
        text: data.content,
        traceId: request.traceId,
        sessionId: request.sessionId,
        requestId: request.requestId,
        timestamp: Date.now(),
      };
    }

    return null;
  }
}
