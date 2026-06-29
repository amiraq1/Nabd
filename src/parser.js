export function parseInstruction(instruction) {
  const lower = instruction.toLowerCase().trim();

  // File skill detection (checked first to avoid shadowing by shell triggers)
  const fileActions = [
    { pattern: /^read file\s+/, action: 'read' },
    { pattern: /^create file\s+/, action: 'create' },
    { pattern: /^delete file\s+/, action: 'delete' },
    { pattern: /^write file\s+/, action: 'write' },
    { pattern: /^append file\s+/, action: 'append' }
  ];

  for (const { pattern, action } of fileActions) {
    if (pattern.test(lower)) {
      const payload = instruction.slice(instruction.toLowerCase().match(pattern)[0].length).trim();
      return {
        description: `File operation: ${action}`,
        steps: [{ skill: 'file', action, payload }]
      };
    }
  }

  // Shell skill detection with natural-language mappings
  const shellMappings = [
    { trigger: 'list files in ', command: 'ls ' },
    { trigger: 'list files', command: 'ls' },
    { trigger: 'list ', command: 'ls ' },
    { trigger: 'show me ', command: 'cat ' },
    { trigger: 'show ', command: 'cat ' },
    { trigger: 'display ', command: 'cat ' },
    { trigger: 'find ', command: 'find ' },
    { trigger: 'print ', command: 'echo ' },
    { trigger: 'echo ', command: 'echo ' },
    { trigger: 'run ', command: '' },
    { trigger: 'execute ', command: '' },
    { trigger: 'exec ', command: '' },
    { trigger: 'get ', command: '' }
  ];

  for (const { trigger, command: prefix } of shellMappings) {
    if (lower.startsWith(trigger)) {
      const rest = instruction.slice(trigger.length).trim();
      const command = (prefix + rest).trim();
      return {
        description: `Run shell command: ${command || instruction}`,
        steps: [{ skill: 'shell', action: 'exec', command: command || instruction }]
      };
    }
  }

  // Fallback to shell for anything else
  return {
    description: `Run shell command: ${instruction}`,
    steps: [{ skill: 'shell', action: 'exec', command: instruction }]
  };
}
