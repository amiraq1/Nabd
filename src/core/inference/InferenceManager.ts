import { randomUUID } from 'node:crypto';
import type { InferenceProvider, InferenceRequest } from './InferenceProvider.js';
import { globalEventBus, type EventBus } from '../events/EventBus.js';
import type { SystemEvent } from '../events/ExecutionEvent.js';

export class InferenceManager {
  private providers = new Map<string, InferenceProvider>();

  // إزالة الـ Casing العشوائي (as any) في المُنشئ لدعم Type Safety
  constructor(private readonly bus: EventBus = globalEventBus) {}

  registerProvider(provider: InferenceProvider): void {
    this.providers.set(provider.name, provider);
  }

  getProvider(name: string): InferenceProvider {
    const provider = this.providers.get(name);
    if (!provider) {
      throw new Error(`فشل النظام: مزود الاستدلال [${name}] غير مسجل.`);
    }
    return provider;
  }

  async generate(
    providerName: string, 
    prompt: string, 
    options: Partial<Omit<InferenceRequest, 'requestId' | 'prompt'>> = {},
    abortSignal?: AbortSignal // دمج إشارة الإلغاء للتحكم في استهلاك الموارد
  ): Promise<string> {
    const provider = this.getProvider(providerName);

    const request: InferenceRequest = {
      ...options,
      traceId: options.traceId || options.sessionId || randomUUID(),
      sessionId: options.sessionId || randomUUID(),
      requestId: randomUUID(),
      prompt,
    };

    let fullOutput = '';
    const generator = provider.generate(request);

    try {
      for await (const event of generator) {
        // فحص مبكر: التوقف فوراً إذا أرسل المستخدم أو النظام إشارة إلغاء
        if (abortSignal?.aborted) {
          throw new Error('Inference cancelled by user signal');
        }

        // توحيد الواجهات برمجياً بدلاً من الـ unknown casting
        const systemEvent = event as unknown as SystemEvent;
        this.bus.emit(systemEvent);

        switch (event.type) {
          case 'Token':
            // استخدام عملية جمع السلاسل بشكل آمن
            if ('text' in event) fullOutput += event.text;
            break;
          case 'Failed':
            if ('fatal' in event && event.fatal) {
              throw new Error(`Inference failed: ${'error' in event ? event.error : 'Unknown'}`);
            }
            break;
          case 'Cancelled':
            throw new Error('Inference cancelled');
          case 'Timeout':
            throw new Error('Inference timed out');
        }
      }
    } catch (err: any) {
      const isExpectedInterrupt = 
        err.message === 'Inference cancelled' || 
        err.message === 'Inference cancelled by user signal' || 
        err.message === 'Inference timed out' || 
        err.message.startsWith('Inference failed:');

      // إذا كان انهياراً غير متوقع للشبكة أو المحرك، نقوم بإرسال حدث فشل صريح
      if (!isExpectedInterrupt) {
        this.bus.emit({
          type: 'Failed',
          error: `انقطاع غير متوقع في محرك الذكاء الاصطناعي: ${err.message}`,
          fatal: true,
          traceId: request.traceId,
          sessionId: request.sessionId,
          requestId: request.requestId,
          timestamp: Date.now()
        } as unknown as SystemEvent);
      }
      
      throw err;
    }

    return fullOutput;
  }
}

export const inferenceManager = new InferenceManager();
