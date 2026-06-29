import type { InferenceCapabilities, InferenceRequest } from '../InferenceProvider.js';
import type { InferenceEvent } from '../InferenceEvents.js';
import { BaseProvider } from './BaseProvider.js';

export class LiteRTProvider extends BaseProvider {
  readonly name = 'LiteRT';
  private baseUrl = process.env.LITERT_URL || 'http://localhost:9090';

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
      supportsGrammar: false,
      supportsJsonMode: false,
      supportsSystemPrompt: false,
      supportsCancellation: true,
      supportsParallelRequests: false,
    };
  }

  protected buildRequest(request: InferenceRequest) {
    return {
      url: `${this.baseUrl}/predict`,
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({
        input: request.prompt,
        stream: true
      })
    };
  }

  protected parseLine(line: string, request: InferenceRequest): InferenceEvent | null {
    if (!line.trim()) return null;
    
    let data;
    try {
      data = JSON.parse(line);
    } catch {
      return null;
    }

    if (data.done) {
      return null; // Handle if LiteRT sends Usage differently
    }

    if (data.text) {
      return {
        type: 'Token',
        text: data.text,
        traceId: request.traceId,
        sessionId: request.sessionId,
        requestId: request.requestId,
        timestamp: Date.now(),
      };
    }

    return null;
  }
}
