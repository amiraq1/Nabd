import { LLMProtocol, type ToolCall } from './LLMProtocol.js';
import { CircuitBreaker } from './CircuitBreaker.js';

export interface PlannerConfig {
  maxIterations: number;
}

export type PlannerDecision =
  | { action: 'CONTINUE'; call: ToolCall }
  | { action: 'STOP'; reason: string }
  | { action: 'RETRY_ERROR'; error: string };

export class Planner {
  private iterations = 0;
  private circuitBreaker = new CircuitBreaker();

  constructor(private config: PlannerConfig = { maxIterations: 30 }) {}

  decide(llmOutput: string): PlannerDecision {
    if (this.iterations >= this.config.maxIterations) {
      return { action: 'STOP', reason: 'Max iterations reached.' };
    }

    if (this.circuitBreaker.isOpen()) {
      return { action: 'STOP', reason: 'Circuit breaker is open due to repeated failures.' };
    }

    this.iterations++;
    
    const call = LLMProtocol.parseToolCall(llmOutput);
    if (!call) {
      this.circuitBreaker.recordFailure();
      return { action: 'RETRY_ERROR', error: 'Malformed tool call.' };
    }

    return { action: 'CONTINUE', call };
  }

  recordToolSuccess(): void {
    this.circuitBreaker.recordSuccess();
  }

  recordToolFailure(): void {
    this.circuitBreaker.recordFailure();
  }

  getIterations(): number {
    return this.iterations;
  }
}
