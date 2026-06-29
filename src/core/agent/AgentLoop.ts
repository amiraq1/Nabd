import { randomUUID } from 'node:crypto';
import type { SecurityContext } from '../security/SecurityContext.js';
import type { ToolEngine } from '../tool-engine.js';
import { Planner } from './Planner.js';
import type { AgentState, AgentEvent, StateTransitionEvent } from './AgentEvents.js';
import { globalEventBus, EventBus } from '../events/EventBus.js';

export interface Checkpoint {
  state: AgentState;
  iterations: number;
  // simplified for checkpointing
  traceId: string;
}

export class AgentLoop {
  private state: AgentState = 'INITIALIZING';
  private planner = new Planner();
  private traceId: string;
  private sessionId: string;

  constructor(
    private readonly toolEngine: ToolEngine,
    private readonly security: SecurityContext,
    private readonly bus: EventBus = globalEventBus
  ) {
    this.traceId = randomUUID();
    this.sessionId = security.sessionId;
  }

  private transition(to: AgentState): void {
    const from = this.state;
    this.state = to;
    this.bus.emit({
      type: 'StateTransition',
      from,
      to,
      traceId: this.traceId,
      sessionId: this.sessionId,
      timestamp: Date.now()
    } as StateTransitionEvent);
  }

  async run(prompt: string): Promise<void> {
    this.transition('BUILDING_PROMPT');
    // ... prompt building logic ...

    while (this.state !== 'TERMINATING') {
      this.transition('WAITING_FOR_LLM');
      
      // simulated LLM call
      const llmOutput = await this.callLLM(prompt);

      this.transition('VALIDATING_REQUEST');
      const decision = this.planner.decide(llmOutput);

      this.bus.emit({
        type: 'PlannerDecision',
        decision: decision.action,
        reason: decision.action === 'STOP' ? decision.reason : undefined,
        traceId: this.traceId,
        sessionId: this.sessionId,
        timestamp: Date.now()
      });

      if (decision.action === 'STOP') {
        this.transition('TERMINATING');
        break;
      }

      if (decision.action === 'RETRY_ERROR') {
        this.transition('DECIDING');
        continue;
      }

      this.transition('EXECUTING');
      const callId = randomUUID();
      this.bus.emit({
        type: 'ToolCall',
        toolName: decision.call.tool,
        arguments: decision.call.arguments,
        callId,
        traceId: this.traceId,
        sessionId: this.sessionId,
        timestamp: Date.now()
      });

      try {
        const result = await this.toolEngine.execute(
          decision.call.tool, 
          decision.call.arguments, 
          { security: this.security }
        );
        this.planner.recordToolSuccess();
      } catch (err) {
        this.planner.recordToolFailure();
      }

      this.transition('OBSERVING');
      this.saveCheckpoint();
    }
  }

  private async callLLM(prompt: string): Promise<string> {
    // Stub
    return JSON.stringify({ tool: 'execute_bash', arguments: { command: 'echo 1' } });
  }

  private cpStore: Checkpoint | null = null;

  private saveCheckpoint(): void {
    this.cpStore = {
      state: this.state,
      iterations: this.planner.getIterations(),
      traceId: this.traceId
    };
  }

  restoreCheckpoint(cp: Checkpoint): void {
    this.state = cp.state;
    this.traceId = cp.traceId;
    // in reality, we'd also restore planner state, but planner is simple here.
    // we could expose a method on Planner to set iterations, or we pass it via config.
    this.planner = new Planner({ maxIterations: 30 }); // reset
    (this.planner as any).iterations = cp.iterations;
  }

  getCheckpoint(): Checkpoint | null {
    return this.cpStore;
  }

  getState(): AgentState {
    return this.state;
  }
}
