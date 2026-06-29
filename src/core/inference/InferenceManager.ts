import { randomUUID } from 'node:crypto';
import type { InferenceProvider, InferenceRequest } from './InferenceProvider.js';
import { globalEventBus, type EventBus } from '../events/EventBus.js';
import { type SystemEvent } from '../events/ExecutionEvent.js';

export class InferenceManager {
  private providers = new Map<string, InferenceProvider>();

  constructor(private readonly bus: EventBus<SystemEvent> = globalEventBus as any) {}

  registerProvider(provider: InferenceProvider): void {
    this.providers.set(provider.name, provider);
  }

  getProvider(name: string): InferenceProvider {
    const provider = this.providers.get(name);
    if (!provider) {
      throw new Error(`InferenceProvider not found: ${name}`);
    }
    return provider;
  }

  async generate(providerName: string, prompt: string, options: Partial<Omit<InferenceRequest, 'requestId' | 'prompt'>> = {}): Promise<string> {
    const provider = this.getProvider(providerName);
    
    const request: InferenceRequest = {
      ...options,
      traceId: options.traceId || options.sessionId || randomUUID(), // using sessionId as trace if none
      sessionId: options.sessionId || randomUUID(),
      requestId: randomUUID(),
      prompt,
    };

    let fullOutput = '';
    const generator = provider.generate(request);

    try {
      for await (const event of generator) {
        // Emit to EventBus
        this.bus.emit(event as unknown as SystemEvent);
        
        if (event.type === 'Token') {
          fullOutput += event.text;
        } else if (event.type === 'Failed' && event.fatal) {
          throw new Error(`Inference failed: ${event.error}`);
        } else if (event.type === 'Cancelled') {
          throw new Error('Inference cancelled');
        } else if (event.type === 'Timeout') {
          throw new Error('Inference timed out');
        }
      }
    } catch (err: any) {
      if (err.message !== 'Inference cancelled' && err.message !== 'Inference timed out' && !err.message.startsWith('Inference failed:')) {
        this.bus.emit({
          type: 'Failed',
          error: err.message,
          fatal: true,
          traceId: request.traceId,
          sessionId: request.sessionId,
          requestId: request.requestId,
          timestamp: Date.now()
        } as unknown as SystemEvent);
        throw err;
      }
      throw err;
    }

    return fullOutput;
  }
}

export const inferenceManager = new InferenceManager();
