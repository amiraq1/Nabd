/**
 * Registry and orchestrator for tool implementations.
 *
 * The engine resolves tool names to {@link ToolDefinition} implementations,
 * merges each tool's policy overrides with a {@link PolicyEngine} default,
 * validates the resulting policy, and dispatches the execution through an
 * {@link ExecutionQueue}. Successful executions return a `ProcessSession`;
 * policy violations are surfaced as a `ProcessSession` already in the
 * `'error'` terminal state and are NOT enqueued.
 */

import type {
  ToolContext,
  ToolDefinition,
} from './types.js';
import { ProcessSession } from './ProcessSession.js';
import { ExecutionQueue } from './ExecutionQueue.js';
import { PolicyEngine } from './PolicyEngine.js';

export class ToolEngine {
  private readonly tools = new Map<string, ToolDefinition>();

  /**
   * @param queue - Execution queue used to schedule tool invocations.
   * @param policyEngine - Policy engine used to merge and validate policies.
   */
  constructor(
    private readonly queue: ExecutionQueue,
    private readonly policyEngine: PolicyEngine,
  ) {}

  /**
   * Registers a tool with the engine.
   *
   * @param tool - The tool definition to register.
   * @throws Error if a tool with the same name has already been registered.
   */
  register(tool: ToolDefinition): void {
    if (this.tools.has(tool.name)) {
      throw new Error(`Tool "${tool.name}" is already registered`);
    }
    this.tools.set(tool.name, tool);
  }

  /**
   * Executes a previously registered tool by name.
   *
   * Behaviour:
   * 1. Looks up the tool by name. Throws if not registered.
   * 2. Merges the tool's `getPolicy(args)` override with the default policy.
   * 3. Validates the resolved policy. On violations, returns a `ProcessSession`
   *    already in the `'error'` state and does NOT enqueue.
   * 4. Otherwise enqueues a factory that invokes `tool.execute(args, context)`
   *    and resolves with the resulting `ProcessSession`.
   *
   * @param name - The name of the tool to invoke.
   * @param args - Tool arguments (shape defined by `tool.parameters`).
   * @param context - Runtime context (streaming callback, abort signal).
   * @returns The tool's `ProcessSession`.
   * @throws Error if no tool with the given name has been registered.
   */
  async execute(
    name: string,
    args: unknown,
    context: ToolContext,
  ): Promise<ProcessSession> {
    const tool = this.tools.get(name);
    if (tool === undefined) {
      throw new Error(`Tool "${name}" is not registered`);
    }

    const policy = this.policyEngine.mergePolicy(
      tool.getPolicy?.(args) ?? {},
    );

    const violations = this.policyEngine.validate(policy, {
      command: tool.name,
      args: [],
      cwd: policy.workingDirectory,
    });

    if (violations.length > 0) {
      return this.buildViolationSession(name, violations);
    }

    return this.queue.enqueue(() => tool.execute(args, context));
  }

  /**
   * Returns the names of all registered tools in lexicographic order.
   */
  list(): string[] {
    return Array.from(this.tools.keys()).sort();
  }

  /**
   * Build a `ProcessSession` already in the `'error'` terminal state whose
   * error message serializes the supplied policy violations. Used to
   * short-circuit enqueue when validation fails.
   */
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

/**
 * Shared {@link ToolEngine} singleton for callers that only need a single
 * registry backed by a default execution queue (concurrency = 3) and the
 * default policy engine.
 */
export const toolEngine = new ToolEngine(
  new ExecutionQueue(3),
  new PolicyEngine(),
);
