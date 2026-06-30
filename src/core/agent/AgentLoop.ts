import { randomUUID } from 'node:crypto';
import type { SecurityContext } from '../security/SecurityContext.js';
import type { ToolEngine } from '../tool-engine.js';
import { Planner } from './Planner.js';
import type { AgentState, StateTransitionEvent } from './AgentEvents.js';
import { globalEventBus, EventBus } from '../events/EventBus.js';
import { isDestructive } from '../security/DestructiveCommandGuard.js';

export interface Checkpoint {
  state: AgentState;
  iterations: number;
  traceId: string;
}

export class AgentLoop {
  private state: AgentState = 'INITIALIZING';
  private planner = new Planner({ maxIterations: 20 }); // وضع سقف افتراضي ذكي
  private traceId: string;
  private sessionId: string;
  private pendingConfirmation: ((res: boolean) => void) | null = null;
  
  // سقوف الأمان الخاصة بالحلقة في بيئة موبايل/Termux
  private readonly MAX_LOOP_ITERATIONS = 20;
  // أضف هذا الثابت أعلى كلاس AgentLoop لحماية ذاكرة الموبايل
  private readonly MAX_HISTORY_TURNS = 6; 
  // مخزن منظم لإدارة الرسائل بدلاً من التراكم النصي العشوائي لمنع Bloat
  private history: Array<{ role: 'user' | 'assistant' | 'system'; content: string }> = [];

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

  /**
   * صياغة النص التوجيهي بناءً على تاريخ الحوار الحالي (Sliding Context Assembly)
   */
  /**
   * صياغة النص التوجيهي مع ضغط السياق (Sliding Context Assembly) لمنع (Context Window Overflow)
   */
  private compilePrompt(systemInstruction: string): string {
    // الاحتفاظ بآخر N رسائل فقط للحفاظ على خفة وسرعة النموذج
    let compressedHistory = this.history;
    if (this.history.length > this.MAX_HISTORY_TURNS) {
      compressedHistory = [
        this.history[0], // الاحتفاظ بالطلب الأصلي دائماً
        ...this.history.slice(-(this.MAX_HISTORY_TURNS - 1)) // جلب أحدث الرسائل
      ];
    }

    const historyStr = compressedHistory
      .map(msg => `${msg.role === 'user' ? 'User Request' : msg.role === 'assistant' ? 'Assistant' : 'System'}: ${msg.content}`)
      .join('\n\n');
      
    return `${systemInstruction}\n\n[CONTEXT COMPRESSED FOR MOBILE MEMORY]\n\n${historyStr}`;
  }

  async run(prompt: string): Promise<void> {
    this.transition('BUILDING_PROMPT');

    const systemInstruction = `You are an autonomous agent in NABD_OS.
For simple greetings, questions, or conversational messages that don't require system access, respond directly with plain text — do NOT call any tool.
Only use a tool when the task genuinely requires file access, command execution, or external data.          
AVAILABLE TOOLS: ${this.toolEngine.list().join(', ')}

For tasks with 3+ distinct steps, call write_todos FIRST with the full plan as a string array. Update items with update_todo as you complete them.
When you DO need a tool, respond with STRICT JSON. Examples:
- {"tool": "execute_bash", "arguments": {"command": "ls -la"}}

When you do NOT need a tool, respond with plain text directly answering the user.`;

    // إعداد تاريخ العمليات للطلب الحالي
    this.history = [{ role: 'user', content: prompt }];
    let loopCounter = 0;

    while (this.state !== 'TERMINATING') {
      // حارس بوابي لمنع الحلقات اللانهائية واستنزاف بطارية الهاتف ورصيد الـ API
      if (loopCounter >= this.MAX_LOOP_ITERATIONS || this.planner.getIterations() >= this.MAX_LOOP_ITERATIONS) {
        this.bus.emit({
          type: 'Failed',
          error: `تم إيقاف الوكيل قسرياً: تجاوز الحد الأقصى للمحاولات المسموح بها في الدورة الحالية (${this.MAX_LOOP_ITERATIONS}).`,
          fatal: true,
          traceId: this.traceId,
          sessionId: this.sessionId,
          timestamp: Date.now()
        } as any);
        this.transition('TERMINATING');
        break;
      }
      
      loopCounter++;
      this.transition('WAITING_FOR_LLM');

      const currentCompiledPrompt = this.compilePrompt(systemInstruction);
      const llmOutput = await this.callLLM(currentCompiledPrompt);
      
      this.history.push({ role: 'assistant', content: llmOutput });

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

      if (decision.action === 'FINAL_ANSWER' || decision.action === 'STOP') {
        this.transition('TERMINATING');
        break;
      }

      if (decision.action === 'RETRY_ERROR') {
        this.history.push({ 
          role: 'system', 
          content: `System Error: ${decision.error} (Your previous output was invalid. You MUST output ONLY valid JSON format).` 
        });
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
        // التحقق من الأوامر التدميرية وحماية النظام
        if (decision.call.tool === 'execute_bash' && decision.call.arguments && isDestructive(decision.call.arguments.command)) {
          this.bus.emit({
            type: 'ConfirmationRequired',
            command: decision.call.arguments.command,
            traceId: this.traceId,
            sessionId: this.sessionId,
            timestamp: Date.now()
          });

          if (this.pendingConfirmation) {
            throw new Error('هنالك تأكيد أمني معلق بالفعل، لا يمكن معالجة أمر تدميري آخر بالتزامن.');
          }

          const confirmed = await new Promise<boolean>(resolve => {
            this.pendingConfirmation = resolve;
          });

          if (!confirmed) {
            this.history.push({ 
              role: 'system', 
              content: `System Tool Error: User declined this command. Choose a safer approach or ask for clarification.` 
            });
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

        const resultStr = typeof result === 'object' && result !== null && 'exitCode' in result
            ? `Exit Code: ${result.exitCode}, Output Bytes: ${result.outputByteCount}`
            : 'Tool executed successfully (Streaming).';
            
        this.history.push({ role: 'system', content: `System Tool Result: ${resultStr}` });
      } catch (err: any) {
        this.planner.recordToolFailure();
        this.history.push({ role: 'system', content: `System Tool Error: ${err.message}` });
      }

      this.transition('OBSERVING');
      this.saveCheckpoint();
    }
  }

  private async callLLM(prompt: string): Promise<string> {
    // استيراد الموديولات بشكل كفؤ والاعتماد على الكاش الداخلي لنظام Node.js
    const [{ inferenceManager }, { globalConfig }] = await Promise.all([
      import('../inference/InferenceManager.js'),
      import('../../GlobalConfig.js')
    ]);
    
    const providerName = globalConfig.provider === 'nvidia' || globalConfig.provider === 'openai' 
      ? 'OpenAICompatible' 
      : 'Ollama';
      
    try {
      return await inferenceManager.generate(providerName, prompt, {
        traceId: this.traceId,
        sessionId: this.sessionId
      });
    } catch (err: any) {
      this.bus.emit({
        type: 'Failed',
        error: `فشل الاتصال بمزود الاستدلال: ${err.message}`,
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

  /**
   * استرجاع نقطة التفتيش بشكل آمن مع حماية الكبسلة
   */
  restoreCheckpoint(cp: Checkpoint): void {
    this.state = cp.state;
    this.traceId = cp.traceId;
    // إعادة بناء المخطط وضبط المحاولات المستهلكة عبر الواجهة الرسمية المحدثة للـ Planner أو تمريرها بالبناء
    this.planner = new Planner({ maxIterations: this.MAX_LOOP_ITERATIONS, initialIterations: cp.iterations });
  }

  getCheckpoint(): Checkpoint | null {
    return this.cpStore;
  }

  getState(): AgentState {
    return this.state;
  }
}
