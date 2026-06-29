import { PermissionDeniedError } from '../errors.js';
import type { ToolDefinition } from '../types.js';
import type { SecurityContext } from '../security/SecurityContext.js';

export class PermissionResolver {
  /**
   * Verifies that the tool's required permissions are satisfied by the SecurityContext.
   * Throws PermissionDeniedError if any required permission is missing or if context is missing.
   */
  verify(tool: ToolDefinition, context?: SecurityContext): void {
    if (!context) {
      throw new PermissionDeniedError(`PermissionResolver: Execution of '${tool.name}' denied. No SecurityContext provided.`);
    }

    const allowed = new Set(context.permissions);
    for (const req of tool.permissions) {
      if (!allowed.has(req)) {
        throw new PermissionDeniedError(`PermissionResolver: Execution of '${tool.name}' denied. Missing required permission: '${req}' for role '${context.role}'`);
      }
    }
  }
}

export const permissionResolver = new PermissionResolver();
