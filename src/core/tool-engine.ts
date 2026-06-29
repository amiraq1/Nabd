/**
 * Registry and orchestrator for tool implementations.
 *
 * The engine resolves tool names to {@link ToolDefinition} implementations via CapabilityResolver,
 * validates arguments via SchemaValidator, verifies permissions,
 * merges each tool's policy overrides with a {@link PolicyEngine} default,
 * validates the resulting policy, and dispatches the execution through an
 * {@link ExecutionQueue}. Successful executions return a `ProcessSession`;
 * policy violations are surfaced as a `ProcessSession` already in the
 * `'error'` terminal state and are NOT enqueued.
 */

import type {
  ToolContext,
  ToolDefinition,
  PermissionLevel,
} from './types.js';
import { ProcessSession } from './ProcessSession.js';
import { ExecutionQueue } from './ExecutionQueue.js';
import { PolicyEngine } from './PolicyEngine.js';
import { globalToolRegistry } from './tools/ToolRegistry.js';
import { capabilityResolver } from './tools/CapabilityResolver.js';
import { schemaValidator } from './tools/SchemaValidator.js';
import { permissionResolver } from './tools/PermissionResolver.js';

export class ToolEngine {
  /**
   * @param queue - Execution queue used to schedule tool invocations.
   * @param policyEngine - Policy engine used to merge and validate policies.
   */
  constructor(
    private readonly queue: ExecutionQueue,
    private readonly policyEngine: PolicyEngine,
    private readonly registry: typeof globalToolRegistry = globalToolRegistry,
    private readonly resolver: typeof capabilityResolver = capabilityResolver
  ) {}

  /**
   * Registers a tool with the engine (delegates to ToolRegistry for backward compatibility).
   *
   * @param tool - The tool definition to register.
   */
  register(tool: ToolDefinition): void {
    this.registry.register(tool);
  }

  /**
   * Executes a previously registered tool by name.
   *
   * @param name - The name of the tool to invoke.
   * @param args - Tool arguments (shape defined by `tool.parameters`).
   * @param context - Runtime context (streaming callback, abort signal, allowedPermissions).
   * @returns The tool's `ProcessSession`.
   */
  async execute(
    name: string,
    args: unknown,
    context: ToolContext & { streamV3: true; allowedPermissions?: PermissionLevel[] },
  ): Promise<AsyncGenerator<import('./events/ExecutionEvent.js').ExecutionEventV3, void, void>>;

  async execute(
    name: string,
    args: unknown,
    context?: ToolContext & { streamV3?: false; allowedPermissions?: PermissionLevel[] },
  ): Promise<import('./types.js').ToolExecutionResult>;

  async execute(
    name: string,
    args: unknown,
    context: ToolContext & { allowedPermissions?: PermissionLevel[] } = {},
  ): Promise<AsyncGenerator<import('./events/ExecutionEvent.js').ExecutionEventV3, void, void> | import('./types.js').ToolExecutionResult> {
    let session: ProcessSession;

    // 1. Resolve capability (handles aliases, etc.)
    const tool = this.resolver.resolve(name);

    // 2. Validate arguments strictly against JSON Schema
    schemaValidator.validate(tool, args);

    // 3. Verify permissions before PolicyEngine
    permissionResolver.verify(tool, context.security);

    // 4. Resolve and validate Policy
    const policy = this.policyEngine.mergePolicy(tool.getPolicy?.(args) ?? {});
    const violations = this.policyEngine.validate(policy, {
      command: tool.name, // policy engine validates the tool's canonical name
      args: [],
      cwd: policy.workingDirectory,
    });

    if (violations.length > 0) {
      session = this.buildViolationSession(name, violations);
    } else {
      // 5. Enqueue execution
      session = await this.queue.enqueue(() => tool.execute(args, context));
    }

    if (context.streamV3) {
      return session.stream();
    }

    if (context.onStream) {
      (async () => {
        for await (const event of session.stream()) {
          if (event.type === 'StdoutChunk') context.onStream!((event as any).chunk, false);
          if (event.type === 'StderrChunk') context.onStream!((event as any).chunk, true);
          if (event.type === 'StdoutBatch') {
            for (const chunk of (event as any).chunks) context.onStream!(chunk, false);
          }
          if (event.type === 'StderrBatch') {
            for (const chunk of (event as any).chunks) context.onStream!(chunk, true);
          }
        }
      })().catch(() => {});
    }

    for await (const _event of session.stream()) {}

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

  /**
   * Returns the names of all registered tools.
   */
  list(): string[] {
    return this.registry.list().map(t => t.name).sort();
  }

  private buildViolationSession(
    toolName: string,
    violations: ReadonlyArray<{ rule: string; message: string }>,
  ): ProcessSession {
    const executionId = `exec-${toolName}-violation-${Date.now()}`;
    const session = new ProcessSession({
      executionId,
      command: toolName,
      args: [],
      cwd: process.cwd(),
    });
    const message = violations
      .map((v) => `[${v.rule}] ${v.message}`)
      .join('; ');
    session.error(new Error(`Policy violation(s): ${message}`));
    return session;
  }
}

export const toolEngine = new ToolEngine(
  new ExecutionQueue(3),
  new PolicyEngine(),
);
