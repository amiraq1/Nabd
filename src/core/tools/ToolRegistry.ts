import type { ToolDefinition } from '../types.js';

export class ToolRegistry {
  private readonly tools = new Map<string, ToolDefinition>();
  private readonly aliases = new Map<string, string>();
  private isFrozen = false;

  register(tool: ToolDefinition): void {
    if (this.isFrozen) {
      throw new Error('ToolRegistry is frozen: cannot register new tools.');
    }
    
    if (this.tools.has(tool.id)) {
      throw new Error(`ToolRegistry: duplicate tool id '${tool.id}'`);
    }

    if (this.tools.has(tool.name) || this.aliases.has(tool.name)) {
      throw new Error(`ToolRegistry: duplicate tool name '${tool.name}'`);
    }

    // Check aliases
    if (tool.aliases) {
      for (const alias of tool.aliases) {
        if (this.tools.has(alias) || this.aliases.has(alias)) {
          throw new Error(`ToolRegistry: duplicate alias '${alias}'`);
        }
      }
    }

    // Make immutable
    const frozenTool = Object.freeze({ ...tool });
    
    this.tools.set(frozenTool.id, frozenTool);
    this.aliases.set(frozenTool.name, frozenTool.id);

    if (frozenTool.aliases) {
      for (const alias of frozenTool.aliases) {
        this.aliases.set(alias, frozenTool.id);
      }
    }
  }

  unregister(id: string): boolean {
    if (this.isFrozen) {
      throw new Error('ToolRegistry is frozen: cannot unregister tools.');
    }

    const tool = this.tools.get(id);
    if (!tool) return false;

    this.tools.delete(id);
    this.aliases.delete(tool.name);
    
    if (tool.aliases) {
      for (const alias of tool.aliases) {
        this.aliases.delete(alias);
      }
    }

    return true;
  }

  get(idOrAlias: string): ToolDefinition | undefined {
    // If it's an exact ID
    let tool = this.tools.get(idOrAlias);
    if (tool) return tool;

    // Resolve via alias
    const resolvedId = this.aliases.get(idOrAlias);
    if (resolvedId) return this.tools.get(resolvedId);

    return undefined;
  }

  list(): ToolDefinition[] {
    return Array.from(this.tools.values());
  }

  exists(idOrAlias: string): boolean {
    return this.tools.has(idOrAlias) || this.aliases.has(idOrAlias);
  }

  freeze(): void {
    this.isFrozen = true;
  }
}

export const globalToolRegistry = new ToolRegistry();
