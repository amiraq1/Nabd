import { randomUUID } from 'node:crypto';
import type { ToolContext, ToolDefinition, PermissionLevel, ToolExecutionResult } from './types.js';
import { ProcessSession } from './ProcessSession.js';
import { ExecutionQueue } from './ExecutionQueue.js';
import { PolicyEngine, policyEngine as globalPolicyEngine } from './PolicyEngine.js';
import { globalToolRegistry } from './tools/ToolRegistry.js';
import { capabilityResolver } from './tools/CapabilityResolver.js';
import { schemaValidator } from './tools/SchemaValidator.js';
import { permissionResolver } from './tools/PermissionResolver.js';
import type { ExecutionEventV3 } from './events/ExecutionEvent.js';

/**
 * Type Guard للتأكد من أن الكائن العائد هو بالفعل جلسة عملية قابلة للتدفق.
 */
function isProcessSession(obj: unknown): obj is ProcessSession {
  return typeof obj === 'object' && obj !== null && 'stream' in obj && 'snapshot' in obj;
}

export class ToolEngine {
  /**
   * تم استبدال إنشاء PolicyEngine جديد باستخدام النسخة العامة (Singleton)
   * لضمان توحيد السياسات على مستوى التطبيق.
   */
  constructor(
    private readonly queue: ExecutionQueue,
    private readonly policyEngine: PolicyEngine = globalPolicyEngine,
    private readonly registry: typeof globalToolRegistry = globalToolRegistry,
    private readonly resolver: typeof capabilityResolver = capabilityResolver
  ) {}

  register(tool: ToolDefinition): void {
    this.registry.register(tool);
  }

  async execute(
    name: string,
    args: unknown,
    context: ToolContext & { streamV3: true; allowedPermissions?: PermissionLevel[] },
  ): Promise<AsyncGenerator<ExecutionEventV3, void, void>>;

  async execute(
    name: string,
    args: unknown,
    context?: ToolContext & { streamV3?: false; allowedPermissions?: PermissionLevel[] },
  ): Promise<ToolExecutionResult>;

  async execute(
    name: string,
    args: unknown,
    context: ToolContext & { allowedPermissions?: PermissionLevel[] } = {},
  ): Promise<AsyncGenerator<ExecutionEventV3, void, void> | ToolExecutionResult> {
    let session: unknown;

    const tool = this.resolver.resolve(name);

    schemaValidator.validate(tool, args);
    permissionResolver.verify(tool, context.security);

    const policy = this.policyEngine.mergePolicy(tool.getPolicy?.(args) ?? {});
    
    // احترام مجلد العمل الآمن بدلاً من القفز لمسار المعالجة الرئيسي
    const workingDirectory = policy.workingDirectory || process.cwd();

    const violations = this.policyEngine.validate(policy, {
      command: tool.name, 
      args: [],
      cwd: workingDirectory,
    });

    if (violations.length > 0) {
      session = this.buildViolationSession(name, violations, workingDirectory);
    } else {
      session = await this.queue.enqueue(() => tool.execute(args, context));
    }

    // إذا كانت الأداة تعيد بيانات مباشرة (Raw Object) وليس جلسة (ProcessSession)
    if (!isProcessSession(session)) {
      return session as ToolExecutionResult;
    }

    if (context.streamV3) {
      return session.stream();
    }

    // معالجة الـ Streams بشفافية دون استثناءات مبطنة
    if (context.onStream) {
      try {
        for await (const event of session.stream()) {
          if (event.type === 'StdoutChunk' && 'chunk' in event) context.onStream(event.chunk as string, false);
          if (event.type === 'StderrChunk' && 'chunk' in event) context.onStream(event.chunk as string, true);
          if (event.type === 'StdoutBatch' && 'chunks' in event) {
            for (const chunk of event.chunks as string[]) context.onStream(chunk, false);
          }
          if (event.type === 'StderrBatch' && 'chunks' in event) {
            for (const chunk of event.chunks as string[]) context.onStream(chunk, true);
          }
        }
      } catch (err) {
        console.warn(`[ToolEngine] حدث خطأ أثناء سحب البيانات من الأداة (${name}):`, err);
      }
    } else {
      // تفريغ التدفق لضمان عدم حدوث Deadlocks في الذاكرة
      try {
        for await (const _event of session.stream()) {}
      } catch (err) {
        // يتم بلع الخطأ هنا عمداً لأن المستخدم لم يطلب التدفق (Fire-and-Forget حقيقي)
      }
    }

    const snap = session.snapshot();
    return {
      executionId: snap.executionId,
      exitCode: snap.exitCode,
      signal: snap.signal,
      durationMs: snap.durationMs ?? 0,
      timedOut: snap.timedOut,
      truncated: snap.truncated,
      outputByteCount: snap.totalBytes,
    };
  }

  list(): string[] {
    return this.registry.list().map(t => t.name).sort();
  }

  private buildViolationSession(
    toolName: string,
    violations: ReadonlyArray<{ rule: string; message: string }>,
    cwd: string
  ): ProcessSession {
    const executionId = `exec-${toolName}-violation-${randomUUID()}`;
    const session = new ProcessSession({
      executionId,
      command: toolName,
      args: [],
      cwd, // استخدام المجلد الآمن المطابق للسياسة
    });
    
    const message = violations.map((v) => `[${v.rule}] ${v.message}`).join('; ');
    session.error(new Error(`تم حظر التنفيذ - انتهاك أمني: ${message}`));
    
    return session;
  }
}

// استخدام النسخة الموحدة لـ PolicyEngine بدلاً من عزلها
export const toolEngine = new ToolEngine(
  new ExecutionQueue(3),
  globalPolicyEngine,
);
