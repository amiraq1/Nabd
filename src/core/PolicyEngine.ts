import path from 'node:path';
import type { ExecutionPolicy, PolicyViolation } from './types.js';

/**
 * Context passed to {@link PolicyEngine.validate} describing the concrete
 * execution request that is being checked against a policy.
 */
export interface PolicyValidationContext {
  /** Executable name or path being invoked. */
  command: string;
  /** Positional arguments passed to the executable. */
  args: string[];
  /** Working directory override requested by the caller. */
  cwd?: string;
}

/** Default execution policy used when none is supplied explicitly. */
const DEFAULT_POLICY: ExecutionPolicy = {
  maxExecutionTimeMs: 45000,
  maxOutputBytes: 20 * 1024 * 1024,
  allowNetwork: true,
  allowFilesystemWrite: true,
  allowDelete: false,
  allowBackgroundProcess: false,
  workingDirectory: process.cwd(),
  environment: { ...process.env },
  allowedCommands: [],
};

/** Hard ceiling for `maxExecutionTimeMs` (10 minutes, in milliseconds). */
const MAX_EXECUTION_TIME_MS = 600000;

/** Hard ceiling for `maxOutputBytes` (100 MB). */
const MAX_OUTPUT_BYTES = 100 * 1024 * 1024;

/**
 * Validates execution requests against an {@link ExecutionPolicy} and merges
 * tool-specific policy overrides with a process-wide default policy.
 *
 * The engine is stateless across `validate` invocations: each call inspects
 * the supplied policy independently and returns the full set of violations.
 * Use {@link mergePolicy} to combine a tool-supplied partial policy with the
 * default policy prior to validation.
 */
export class PolicyEngine {
  private defaultPolicy: ExecutionPolicy;

  /**
   * @param defaultPolicy Optional partial policy whose values override the
   * built-in defaults. Unspecified fields fall back to the built-in defaults.
   */
  constructor(defaultPolicy?: Partial<ExecutionPolicy>) {
    this.defaultPolicy = this.buildPolicy(defaultPolicy ?? {});
  }

  /**
   * Validates an execution request against the supplied policy.
   *
   * @param policy  The fully-resolved policy to validate against. Use
   *                {@link mergePolicy} to compose it from a tool override.
   * @param context Description of the execution being requested.
   * @returns A list of violations. An empty list means the request is allowed.
   */
  validate(
    policy: ExecutionPolicy,
    context: PolicyValidationContext,
  ): PolicyViolation[] {
    const violations: PolicyViolation[] = [];

    if (
      !Number.isInteger(policy.maxExecutionTimeMs) ||
      policy.maxExecutionTimeMs <= 0
    ) {
      violations.push({
        rule: 'maxExecutionTimeMs',
        message:
          'maxExecutionTimeMs must be a positive integer (got ' +
          `${String(policy.maxExecutionTimeMs)}).`,
      });
    } else if (policy.maxExecutionTimeMs > MAX_EXECUTION_TIME_MS) {
      violations.push({
        rule: 'maxExecutionTimeMs',
        message:
          `maxExecutionTimeMs must be <= ${MAX_EXECUTION_TIME_MS} ` +
          `(got ${policy.maxExecutionTimeMs}).`,
      });
    }

    if (
      !Number.isInteger(policy.maxOutputBytes) ||
      policy.maxOutputBytes <= 0
    ) {
      violations.push({
        rule: 'maxOutputBytes',
        message:
          'maxOutputBytes must be a positive integer (got ' +
          `${String(policy.maxOutputBytes)}).`,
      });
    } else if (policy.maxOutputBytes > MAX_OUTPUT_BYTES) {
      violations.push({
        rule: 'maxOutputBytes',
        message:
          `maxOutputBytes must be <= ${MAX_OUTPUT_BYTES} ` +
          `(got ${policy.maxOutputBytes}).`,
      });
    }

    if (
      typeof policy.workingDirectory !== 'string' ||
      policy.workingDirectory.length === 0
    ) {
      violations.push({
        rule: 'workingDirectory',
        message: 'workingDirectory must be a non-empty string.',
      });
    }

    const allowed = policy.allowedCommands;
    if (Array.isArray(allowed) && allowed.length > 0) {
      const requested = context.command;
      const requestedBase = path.basename(requested);
      const matched = allowed.some((entry) => {
        if (typeof entry !== 'string' || entry.length === 0) {
          return false;
        }
        if (entry === requested) {
          return true;
        }
        return path.basename(entry) === requestedBase;
      });
      if (!matched) {
        violations.push({
          rule: 'allowedCommands',
          message:
            `command "${requested}" is not in the allowedCommands list ` +
            `[${allowed.join(', ')}].`,
        });
      }
    }

    return violations;
  }

  /**
   * Merges a tool-specific policy with the default policy.
   *
   * Primitives and arrays supplied by the tool override the defaults; objects
   * are shallow-merged (defaults remain for keys not present in the tool
   * policy). Unspecified tool fields fall back to the defaults.
   *
   * @param toolPolicy Partial policy supplied by a tool implementation.
   * @returns A fully-resolved {@link ExecutionPolicy}.
   */
  mergePolicy(toolPolicy: Partial<ExecutionPolicy>): ExecutionPolicy {
    return this.buildPolicy(toolPolicy);
  }

  /**
   * Builds a complete {@link ExecutionPolicy} by applying the supplied
   * overrides on top of the instance's default policy.
   */
  private buildPolicy(overrides: Partial<ExecutionPolicy>): ExecutionPolicy {
    // `this.defaultPolicy` is not yet assigned during the constructor's
    // initial `buildPolicy` call, so fall back to the module-level defaults
    // when the instance field is undefined.
    const base: ExecutionPolicy = this.defaultPolicy ?? DEFAULT_POLICY;
    const mergedEnvironment: Record<string, string | undefined> = {
      ...base.environment,
      ...(overrides.environment ?? {}),
    };

    return {
      maxExecutionTimeMs:
        overrides.maxExecutionTimeMs ?? base.maxExecutionTimeMs,
      maxOutputBytes: overrides.maxOutputBytes ?? base.maxOutputBytes,
      allowNetwork: overrides.allowNetwork ?? base.allowNetwork,
      allowFilesystemWrite:
        overrides.allowFilesystemWrite ?? base.allowFilesystemWrite,
      allowDelete: overrides.allowDelete ?? base.allowDelete,
      allowBackgroundProcess:
        overrides.allowBackgroundProcess ?? base.allowBackgroundProcess,
      workingDirectory: overrides.workingDirectory ?? base.workingDirectory,
      environment: mergedEnvironment,
      allowedCommands: overrides.allowedCommands ?? base.allowedCommands,
    };
  }
}

/** Process-wide singleton instance of {@link PolicyEngine}. */
export const policyEngine = new PolicyEngine();
