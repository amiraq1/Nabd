import crypto from 'node:crypto';
import type { ToolDefinition } from '../types.js';
import { globalToolRegistry, ToolRegistry } from './ToolRegistry.js';

export class ManifestGenerator {
  constructor(private readonly registry: ToolRegistry = globalToolRegistry) {}

  /**
   * Retrieves tools filtered by visibility.
   * By default, returns only 'stable' tools.
   */
  getVisibleTools(includeHidden = false): ToolDefinition[] {
    const all = this.registry.list();
    if (includeHidden) return all;
    return all.filter(t => t.visibility === 'stable' || t.visibility === 'experimental' || t.visibility === 'deprecated').filter(t => t.visibility === 'stable'); // wait, requirement: "Only stable tools appear in default manifests."
  }

  generateJSON(includeHidden = false): string {
    const tools = this.getVisibleTools(includeHidden);
    const manifest = tools.map(this.mapToManifestItem);
    return JSON.stringify(manifest, null, 2);
  }

  generateYAML(includeHidden = false): string {
    const tools = this.getVisibleTools(includeHidden);
    const manifest = tools.map(this.mapToManifestItem);
    
    // Very simple YAML generator for the specific structure
    let yaml = '';
    for (const tool of manifest) {
      yaml += `- name: ${tool.name}\n`;
      yaml += `  category: ${tool.category}\n`;
      yaml += `  description: ${JSON.stringify(tool.description)}\n`;
      yaml += `  permissions: [${tool.permissions.join(', ')}]\n`;
      yaml += `  parameters: ${JSON.stringify(tool.parameters)}\n`;
      if (tool.returns) yaml += `  returns: ${JSON.stringify(tool.returns)}\n`;
      if (tool.examples && tool.examples.length > 0) {
        yaml += `  examples:\n`;
        for (const ex of tool.examples) {
          yaml += `    - ${JSON.stringify(ex)}\n`;
        }
      }
    }
    return yaml;
  }

  generateCompactPrompt(includeHidden = false): string {
    const tools = this.getVisibleTools(includeHidden);
    let prompt = '';
    for (const tool of tools) {
      prompt += `Tool: ${tool.name} (${tool.category}) [${tool.permissions.join(',')}]\n`;
      prompt += `Desc: ${tool.description}\n`;
      prompt += `Args: ${JSON.stringify(tool.parameters)}\n`;
      if (tool.returns) prompt += `Rtrn: ${JSON.stringify(tool.returns)}\n`;
      if (tool.examples && tool.examples.length > 0) prompt += `Ex: ${tool.examples.join(' | ')}\n`;
      prompt += `\n`;
    }
    return prompt.trim();
  }

  generateFingerprint(includeHidden = false): string {
    const json = this.generateJSON(includeHidden);
    return crypto.createHash('sha256').update(json).digest('hex');
  }

  private mapToManifestItem(tool: ToolDefinition) {
    return {
      name: tool.name,
      description: tool.description,
      category: tool.category,
      parameters: tool.parameters,
      returns: tool.returns,
      examples: tool.examples,
      permissions: tool.permissions,
    };
  }
}

export const manifestGenerator = new ManifestGenerator();
