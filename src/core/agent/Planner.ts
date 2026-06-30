import { LLMProtocol, type ToolCall } from './LLMProtocol.js';
import { createHash } from 'node:crypto';

export interface PlannerConfig {
  maxIterations: number;
  maxRepeatedCalls?: number; // جديد
}

export type PlannerDecision =
  | { action: 'CONTINUE'; call: ToolCall }
  | { action: 'FINAL_ANSWER'; text: string }
  | { action: 'STOP'; reason: string }
  | { action: 'RETRY_ERROR'; error: string };

export class Planner {
  private iterations = 0;
  private consecutiveErrors = 0;
  private lastCallHash: string | null = null;
  private repeatedCallCount = 0;

  constructor(
    private config: PlannerConfig = { maxIterations: 30, maxRepeatedCalls: 2 }
  ) {}

  private hashCall(call: ToolCall): string {
    return createHash('sha256')
      .update(call.tool + JSON.stringify(call.arguments))
      .digest('hex');
  }

  decide(llmOutput: string): PlannerDecision {
    if (this.iterations >= this.config.maxIterations) {
      return { action: 'STOP', reason: 'Max iterations reached.' };
    }

    if (this.consecutiveErrors >= 3) {
      return { action: 'STOP', reason: 'Stopped due to repeated malformed tool calls.' };
    }

    this.iterations++;

    const parsed = LLMProtocol.parse(llmOutput);

    if (parsed.kind === 'final_answer') {
      return { action: 'FINAL_ANSWER', text: parsed.text };
    }

    // parsed.kind === 'tool_call'
    const call = parsed.call;
    const callHash = this.hashCall(call);
    if (callHash === this.lastCallHash) {
      this.repeatedCallCount++;
      if (this.repeatedCallCount >= (this.config.maxRepeatedCalls ?? 2)) {
        return { action: 'STOP', reason: `Ghost call: '${call.tool}' repeated.` };
      }
    } else {
      this.repeatedCallCount = 0;
      this.lastCallHash = callHash;
    }

    this.consecutiveErrors = 0;
    return { action: 'CONTINUE', call };
  }

  recordToolSuccess(): void {
    this.consecutiveErrors = 0;
  }

  recordToolFailure(): void {
    // فشل التنفيذ لا يُحسب كخطأ تنسيق — لكن لو الأداة تفشل
    // بنفس الطريقة مراراً، الـ Ghost Call Detection فوق يلتقطه
  }

  getIterations(): number {
    return this.iterations;
  }
}
