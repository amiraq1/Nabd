export type CircuitState = 'Closed' | 'Open' | 'HalfOpen';

export interface CircuitBreakerConfig {
  failureThreshold: number;
  resetTimeoutMs: number;
}

export class CircuitBreaker {
  private state: CircuitState = 'Closed';
  private failures = 0;
  private nextAttemptAt = 0;

  constructor(private config: CircuitBreakerConfig = { failureThreshold: 3, resetTimeoutMs: 5000 }) {}

  getState(): CircuitState {
    if (this.state === 'Open') {
      if (Date.now() >= this.nextAttemptAt) {
        this.state = 'HalfOpen';
      }
    }
    return this.state;
  }

  recordSuccess(): void {
    this.failures = 0;
    this.state = 'Closed';
  }

  recordFailure(): void {
    this.failures++;
    if (this.failures >= this.config.failureThreshold) {
      this.state = 'Open';
      this.nextAttemptAt = Date.now() + this.config.resetTimeoutMs;
    }
  }

  isOpen(): boolean {
    return this.getState() === 'Open';
  }
}
