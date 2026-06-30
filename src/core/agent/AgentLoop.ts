import { randomUUID } from 'node:crypto';
import type { SecurityContext } from '../security/SecurityContext.js';
import type { ToolEngine } from '../tool-engine.js';
import { Planner } from './Planner.js';
import type { AgentState, AgentEvent, StateTransitionEvent } from './AgentEvents.js';
import { globalEventBus, EventBus } from '../events/EventBus.js';
import { isDestructive } from '../security/DestructiveCommandGuard.js';

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
  private pendingConfirmation: ((res: boolean) => void) | null = null;

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

  public confirmCommand(res: boolean): void {
    if (this.pendingConfirmation) {
      this.pendingConfirmation(res);
      this.pendingConfirmation = null;
    }
  }

  async run(prompt: string): Promise<void> {
    this.transition('BUILDING_PROMPT');
    
    let currentPrompt = `You are an autonomous agent in NABD_OS.

For simple greetings, questions, or conversational messages that don't require system access, respond directly with plain text — do NOT call any tool.

Only use a tool when the task genuinely requires file access, command execution, or external data.

AVAILABLE TOOLS: ${this.toolEngine.list().join(', ')}

For tasks with 3+ distinct steps, call write_todos FIRST with the full plan as a string array. Update items with update_todo as you complete them. This is for tracking only — it does not replace actual tool calls.

When you DO need a tool, respond with STRICT JSON. Examples:
- {"tool": "execute_bash", "arguments": {"command": "ls -la"}}
- {"tool": "file_read", "arguments": {"path": "package.json"}}
- {"tool": "list_dir", "arguments": {"path": "src/core"}}

When you do NOT need a tool, respond with plain text directly answering the user.

User Request: ${prompt}`;

    while (this.state !== 'TERMINATING') {
      this.transition('WAITING_FOR_LLM');
      
      const llmOutput = await this.callLLM(currentPrompt);
      currentPrompt += `\n\nAssistant: ${llmOutput}`;

      this.transition('VALIDATING_REQUEST');
      const decision = this.planner.decide(llmOutput);

      const mappedAction = decision.action === 'FINAL_ANSWER' ? 'STOP' : decision.action;
      const mappedReason = decision.action === 'FINAL_ANSWER' ? decision.text : (decision.action === 'STOP' ? decision.reason : undefined);

      this.bus.emit({
        type: 'PlannerDecision',
        decision: mappedAction,
        reason: mappedReason,
        traceId: this.traceId,
        sessionId: this.sessionId,
        timestamp: Date.now()
      });

      if (decision.action === 'FINAL_ANSWER') {
        this.transition('TERMINATING');
        break;
      }

      if (decision.action === 'STOP') {
        this.transition('TERMINATING');
        break;
      }

      if (decision.action === 'RETRY_ERROR') {
        currentPrompt += `\n\nSystem Error: ${decision.error} (Your previous output was invalid. You MUST output ONLY valid JSON format).`;
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
        if (decision.call.tool === 'execute_bash' && decision.call.arguments && isDestructive(decision.call.arguments.command)) {
          this.bus.emit({
            type: 'ConfirmationRequired',
            command: decision.call.arguments.command,
            traceId: this.traceId,
            sessionId: this.sessionId,
            timestamp: Date.now()
          });

          const confirmed = await new Promise<boolean>(resolve => {
            this.pendingConfirmation = resolve;
          });

          if (!confirmed) {
            currentPrompt += `\n\nSystem Tool Error: User declined this command. Choose a safer approach or ask for clarification.`;
            this.transition('DECIDING');
            continue;
          }
        }

        const result = await this.toolEngine.execute(
          decision.call.tool, 
          decision.call.arguments, 
          { security: this.security }
        );
        this.planner.recordToolSuccess();

        this.bus.emit({
          type: 'ToolResult',
          toolName: decision.call.tool,
          result: result,
          callId,
          traceId: this.traceId,
          sessionId: this.sessionId,
          timestamp: Date.now()
        });

        const resultStr = typeof result === 'object' && 'exitCode' in result 
            ? `Exit Code: ${result.exitCode}, Output Bytes: ${result.outputByteCount}`
            : 'Tool executed successfully (Streaming).';
        currentPrompt += `\n\nSystem Tool Result: ${resultStr}`;
      } catch (err: any) {
        this.planner.recordToolFailure();
        currentPrompt += `\n\nSystem Tool Error: ${err.message}`;
      }

      this.transition('OBSERVING');
      this.saveCheckpoint();
    }
  }

  private async callLLM(prompt: string): Promise<string> {
    const { inferenceManager } = await import('../inference/InferenceManager.js');
    const { globalConfig } = await import('../../GlobalConfig.js');
    const providerName = globalConfig.provider === 'nvidia' || globalConfig.provider === 'openai' ? 'OpenAICompatible' : 'Ollama';
    try {
      return await inferenceManager.generate(providerName, prompt, { 
        traceId: this.traceId, 
        sessionId: this.sessionId 
      });
    } catch (err: any) {
      this.bus.emit({
        type: 'Failed',
        error: `Inference Connection Failed: ${err.message}`,
        fatal: true,
        traceId: this.traceId,
        sessionId: this.sessionId,
        requestId: randomUUID(),
        timestamp: Date.now()
      } as any);
      throw err;
    }
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
