import { ToolNotFoundError } from '../errors.js';
import type { ToolDefinition } from '../types.js';
import { globalToolRegistry, ToolRegistry } from './ToolRegistry.js';

export class CapabilityResolver {
  constructor(private readonly registry: ToolRegistry = globalToolRegistry) {}

  /**
   * Resolves a tool name or alias to its canonical ToolDefinition.
   * Throws ToolNotFoundError if the tool cannot be found.
   */
  resolve(nameOrAlias: string): ToolDefinition {
    const tool = this.registry.get(nameOrAlias);
    if (!tool) {
      throw new ToolNotFoundError(`CapabilityResolver: Unknown tool or alias '${nameOrAlias}'`);
    }
    return tool;
  }
}

export const capabilityResolver = new CapabilityResolver();
