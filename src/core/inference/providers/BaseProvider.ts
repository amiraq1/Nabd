import type { IncomingMessage } from 'node:http';
import type { InferenceProvider, InferenceCapabilities, InferenceRequest } from '../InferenceProvider.js';
import type { InferenceEvent } from '../InferenceEvents.js';
import { connectionPool, type InferenceConnectionPool } from '../InferenceConnectionPool.js';

export abstract class BaseProvider implements InferenceProvider {
  abstract readonly name: string;
  protected pool: InferenceConnectionPool = connectionPool;

  async initialize(): Promise<void> {}
  async shutdown(): Promise<void> {}

  abstract health(): Promise<boolean>;
  abstract capabilities(): InferenceCapabilities;

  // Transforms generic InferenceRequest into provider-specific request parameters
  protected abstract buildRequest(request: InferenceRequest): { url: string; method: string; headers: Record<string, string>; body: string };

  // Transforms a single line of SSE / JSON-L from the provider into an InferenceEvent
  // Returns null if the line should be ignored.
  protected abstract parseLine(line: string, request: InferenceRequest): InferenceEvent | null;

  cancel(requestId: string): void {
    // Rely on AbortSignal in request for cancellation
  }

  async *generate(request: InferenceRequest): AsyncGenerator<InferenceEvent, void, unknown> {
    const { url, method, headers, body } = this.buildRequest(request);
    let res: IncomingMessage;

    yield { type: 'InferenceStarted', provider: this.name, traceId: request.traceId, sessionId: request.sessionId, requestId: request.requestId, timestamp: Date.now() } as InferenceEvent;

    try {
      res = await this.pool.request(new URL(url), {
        method,
        headers,
        signal: request.signal,
      }, body);
    } catch (err: any) {
      if (err.name === 'AbortError' || request.signal?.aborted) {
        yield { type: 'Cancelled', traceId: request.traceId, sessionId: request.sessionId, requestId: request.requestId, timestamp: Date.now() } as InferenceEvent;
        return;
      }
      yield { type: 'Failed', error: err.message, fatal: true, traceId: request.traceId, sessionId: request.sessionId, requestId: request.requestId, timestamp: Date.now() } as InferenceEvent;
      return;
    }

    if (res.statusCode && (res.statusCode < 200 || res.statusCode >= 300)) {
      yield { type: 'Failed', error: `HTTP ${res.statusCode}`, fatal: true, traceId: request.traceId, sessionId: request.sessionId, requestId: request.requestId, timestamp: Date.now() } as InferenceEvent;
      res.resume();
      return;
    }

    res.setEncoding('utf8');

    let buffer = '';
    let isDone = false;
    let streamError: Error | null = null;
    let linesQueue: string[] = [];
    
    // Notify mechanism for async generator
    let resolveNext: (() => void) | null = null;

    res.on('data', (chunk: string) => {
      buffer += chunk;
      let nlIndex: number;
      while ((nlIndex = buffer.indexOf('\n')) !== -1) {
        const line = buffer.slice(0, nlIndex).trim();
        buffer = buffer.slice(nlIndex + 1);
        if (line) linesQueue.push(line);
      }
      if (linesQueue.length > 50) {
        res.pause(); // Backpressure
      }
      if (resolveNext && linesQueue.length > 0) {
        resolveNext();
        resolveNext = null;
      }
    });

    res.on('end', () => {
      if (buffer.trim()) linesQueue.push(buffer.trim());
      isDone = true;
      if (resolveNext) { resolveNext(); resolveNext = null; }
    });

    res.on('error', (err) => {
      streamError = err;
      isDone = true;
      if (resolveNext) { resolveNext(); resolveNext = null; }
    });

    request.signal?.addEventListener('abort', () => {
      res.destroy(new Error('AbortError'));
    }, { once: true });

    let fullText = '';

    while (!isDone || linesQueue.length > 0) {
      if (linesQueue.length === 0) {
        await new Promise<void>((resolve) => {
          resolveNext = resolve;
        });
      }

      while (linesQueue.length > 0) {
        const line = linesQueue.shift()!;
        if (linesQueue.length < 25 && res.isPaused()) {
          res.resume(); // Resume stream when buffer drains
        }

        try {
          const event = this.parseLine(line, request);
          if (event) {
            if (event.type === 'Token') {
              fullText += event.text;
            }
            yield event;
          }
        } catch (err: any) {
          // Ignore partial unparseable lines or emit non-fatal error
        }
      }

      if ((streamError as any)) {
        if ((streamError as any).message === 'AbortError' || request.signal?.aborted) {
          yield { type: 'Cancelled', traceId: request.traceId, sessionId: request.sessionId, requestId: request.requestId, timestamp: Date.now() } as InferenceEvent;
          return;
        }
        yield { type: 'Failed', error: (streamError as any).message, fatal: false, traceId: request.traceId, sessionId: request.sessionId, requestId: request.requestId, timestamp: Date.now() } as InferenceEvent;
        return;
      }
    }

    if (!request.signal?.aborted) {
      yield { type: 'Completed', fullText, traceId: request.traceId, sessionId: request.sessionId, requestId: request.requestId, timestamp: Date.now() } as InferenceEvent;
    }
  }
}
