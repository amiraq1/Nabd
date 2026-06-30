import type { InferenceCapabilities, InferenceRequest } from '../InferenceProvider.js';
import type { InferenceEvent } from '../InferenceEvents.js';
import { BaseProvider } from './BaseProvider.js';
import { globalConfig } from '../../../GlobalConfig.js';

export class OpenAICompatibleProvider extends BaseProvider {
  readonly name = 'OpenAICompatible';
  private baseUrl = globalConfig.endpoint || process.env.OPENAI_BASE_URL || 'https://api.openai.com/v1';
  private apiKey = globalConfig.nvidiaApiKey || globalConfig.openaiApiKey || process.env.OPENAI_API_KEY || '';
  private model = globalConfig.model || process.env.OPENAI_MODEL || 'gpt-4o-mini';

  async health(): Promise<boolean> {
    return this.pool.healthCheck(`${this.baseUrl}/models`);
  }

  capabilities(): InferenceCapabilities {
    return {
      supportsStreaming: true,
      supportsVision: true,
      supportsTools: true,
      supportsReasoning: false,
      supportsEmbeddings: true,
      supportsGrammar: false,
      supportsJsonMode: true,
      supportsSystemPrompt: true,
      supportsCancellation: true,
      supportsParallelRequests: true,
    };
  }

  protected buildRequest(request: InferenceRequest) {
    if (!this.apiKey) {
      throw new Error('فشل إرسال الطلب: مفتاح الـ API غير مهيأ (Empty API Key).');
    }
    
    const messages = [];
    if (request.systemPrompt) {
      messages.push({ role: 'system', content: request.systemPrompt });
    }
    messages.push({ role: 'user', content: request.prompt });

    const payload: any = {
      model: this.model,
      messages,
      stream: true,
    };

    if (request.jsonMode) {
      payload.response_format = { type: 'json_object' };
    }

    return {
      url: `${this.baseUrl}/chat/completions`,
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${this.apiKey}`
      },
      body: JSON.stringify(payload)
    };
  }

  protected parseLine(line: string, request: InferenceRequest): InferenceEvent | null {
    if (!line.startsWith('data: ')) return null;
    const dataStr = line.slice(6).trim();
    if (dataStr === '[DONE]') return null;

    const data = JSON.parse(dataStr);
    
    if (data.usage) {
      return {
        type: 'Usage',
        promptTokens: data.usage.prompt_tokens || 0,
        completionTokens: data.usage.completion_tokens || 0,
        totalTokens: data.usage.total_tokens || 0,
        traceId: request.traceId,
        sessionId: request.sessionId,
        requestId: request.requestId,
        timestamp: Date.now(),
      };
    }

    const choice = data.choices?.[0];
    if (choice?.delta?.content) {
      return {
        type: 'Token',
        text: choice.delta.content,
        traceId: request.traceId,
        sessionId: request.sessionId,
        requestId: request.requestId,
        timestamp: Date.now(),
      };
    }

    if (choice?.delta?.tool_calls) {
      const tc = choice.delta.tool_calls[0];
      if (tc.function?.name) {
        return {
          type: 'ToolCallDetected',
          toolName: tc.function.name,
          traceId: request.traceId,
          sessionId: request.sessionId,
          requestId: request.requestId,
          timestamp: Date.now(),
        };
      }
    }

    return null;
  }
}
