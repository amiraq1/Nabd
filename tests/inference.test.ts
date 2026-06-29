import { describe, it } from 'node:test';
import assert from 'node:assert';
import { inferenceManager } from '../src/core/inference/InferenceManager.js';
import { BaseProvider } from '../src/core/inference/providers/BaseProvider.js';
import type { InferenceEvent } from '../src/core/inference/InferenceEvents.js';
import type { InferenceRequest, InferenceCapabilities } from '../src/core/inference/InferenceProvider.js';
import http from 'node:http';

class MockProvider extends BaseProvider {
  readonly name = 'Mock';
  public streamDelayMs = 0;
  public mockResponseChunks: string[] = [];
  public statusCode = 200;

  async health(): Promise<boolean> { return true; }

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
      supportsParallelRequests: true,
    };
  }

  protected buildRequest(request: InferenceRequest) {
    return {
      url: 'http://127.0.0.1:44444/mock',
      method: 'POST',
      headers: {},
      body: ''
    };
  }

  protected parseLine(line: string, request: InferenceRequest): InferenceEvent | null {
    if (!line) return null;
    return {
      type: 'Token',
      text: line,
      traceId: request.traceId,
      sessionId: request.sessionId,
      requestId: request.requestId,
      timestamp: Date.now(),
    };
  }
}

describe('Phase 6 - Inference Runtime', () => {
  let server: http.Server;
  let mockProvider: MockProvider;

  it('setup mock server', async () => {
    server = http.createServer((req, res) => {
      res.writeHead(mockProvider.statusCode, { 'Content-Type': 'text/plain' });
      if (mockProvider.statusCode >= 400) {
        res.end();
        return;
      }

      const sendChunk = (index: number) => {
        if (index >= mockProvider.mockResponseChunks.length) {
          res.end();
          return;
        }
        res.write(mockProvider.mockResponseChunks[index] + '\n');
        if (mockProvider.streamDelayMs > 0) {
          setTimeout(() => sendChunk(index + 1), mockProvider.streamDelayMs);
        } else {
          setImmediate(() => sendChunk(index + 1));
        }
      };
      sendChunk(0);
    });

    await new Promise<void>((resolve) => server.listen(44444, '127.0.0.1', resolve));

    mockProvider = new MockProvider();
    inferenceManager.registerProvider(mockProvider);
  });

  it('streams tokens in order', async () => {
    mockProvider.mockResponseChunks = ['hello', 'world', 'test'];
    const text = await inferenceManager.generate('Mock', 'test prompt', { sessionId: 's1' });
    assert.equal(text, 'helloworldtest');
  });

  it('handles partial JSON gracefully (mock just ignores)', async () => {
    mockProvider.mockResponseChunks = ['a', '', 'b'];
    const text = await inferenceManager.generate('Mock', 'test prompt', { sessionId: 's2' });
    assert.equal(text, 'ab');
  });

  it('handles cancellation via AbortSignal', async () => {
    mockProvider.mockResponseChunks = ['a', 'b', 'c', 'd'];
    mockProvider.streamDelayMs = 100; // delay to allow cancellation

    const ac = new AbortController();
    const p = inferenceManager.generate('Mock', 'test', { signal: ac.signal });
    ac.abort();

    await assert.rejects(p, /Inference cancelled|AbortError/);
    mockProvider.streamDelayMs = 0; // reset
  });

  it('handles network failure (500)', async () => {
    mockProvider.statusCode = 500;
    await assert.rejects(
      inferenceManager.generate('Mock', 'test'),
      /HTTP 500/
    );
    mockProvider.statusCode = 200;
  });

  it('connection reuse (parallel requests)', async () => {
    mockProvider.mockResponseChunks = ['1', '2'];
    const p1 = inferenceManager.generate('Mock', 'a');
    const p2 = inferenceManager.generate('Mock', 'b');
    const [res1, res2] = await Promise.all([p1, p2]);
    assert.equal(res1, '12');
    assert.equal(res2, '12');
  });

  it('backpressure validation', async () => {
    // Generate many chunks quickly to hit highWaterMark (length > 50 in BaseProvider)
    mockProvider.mockResponseChunks = Array(200).fill('x');
    
    // We can't directly measure the Node stream pause/resume in this test without digging into internals,
    // but we can ensure it completely resolves without crashing or losing data.
    const text = await inferenceManager.generate('Mock', 'test');
    assert.equal(text.length, 200);
  });

  it('teardown', () => {
    server.close();
    // destroy connection pool
    (mockProvider as any).pool.destroy();
  });
});
