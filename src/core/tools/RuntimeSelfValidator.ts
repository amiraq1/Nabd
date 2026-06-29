import { globalToolRegistry, ToolRegistry } from './ToolRegistry.js';
import type { ToolDefinition } from '../types.js';

const KNOWN_PERMISSIONS = new Set(['safe', 'filesystem', 'network', 'system', 'dangerous']);

export function validateRuntime(registry: ToolRegistry = globalToolRegistry): void {
  const tools = registry.list();
  
  const ids = new Set<string>();
  const namesAndAliases = new Set<string>();

  for (const tool of tools) {
    // 1. Duplicate IDs
    if (ids.has(tool.id)) {
      throw new Error(`RuntimeValidation: Duplicate tool ID found: '${tool.id}'`);
    }
    ids.add(tool.id);

    // 2. Duplicate Names/Aliases
    if (namesAndAliases.has(tool.name)) {
      throw new Error(`RuntimeValidation: Duplicate tool name/alias found: '${tool.name}'`);
    }
    namesAndAliases.add(tool.name);

    if (tool.aliases) {
      for (const alias of tool.aliases) {
        if (namesAndAliases.has(alias)) {
          throw new Error(`RuntimeValidation: Duplicate tool name/alias found: '${alias}'`);
        }
        namesAndAliases.add(alias);
      }
    }

    // 3. Schema valid
    if (!tool.parameters || typeof tool.parameters !== 'object') {
      throw new Error(`RuntimeValidation: Tool '${tool.name}' has invalid parameters schema.`);
    }

    // 4. Permissions valid
    if (!Array.isArray(tool.permissions)) {
      throw new Error(`RuntimeValidation: Tool '${tool.name}' permissions must be an array.`);
    }
    for (const perm of tool.permissions) {
      if (!KNOWN_PERMISSIONS.has(perm)) {
        throw new Error(`RuntimeValidation: Tool '${tool.name}' has invalid permission: '${perm}'`);
      }
    }
    
    // 5. Policy exists (can invoke getPolicy if provided)
    if (tool.getPolicy) {
      try {
        const policy = tool.getPolicy({});
        if (!policy || typeof policy !== 'object') {
          throw new Error(`RuntimeValidation: Tool '${tool.name}' getPolicy returned invalid policy.`);
        }
      } catch (e: any) {
        throw new Error(`RuntimeValidation: Tool '${tool.name}' getPolicy crashed during validation: ${e.message}`);
      }
    }
  }
}
