export interface ToolCall {
  tool: string;
  arguments: any;
}

export type ParsedOutput =
  | { kind: 'tool_call'; call: ToolCall }
  | { kind: 'final_answer'; text: string };

export class LLMProtocol {
  static parse(output: string): ParsedOutput {
    const call = this.parseToolCall(output);
    if (call) {
      return { kind: 'tool_call', call };
    }
    // لا يوجد tool call صالح — اعتبره إجابة نهائية
    return { kind: 'final_answer', text: output.trim() };
  }

  /**
   * Parses LLM output into a typed ToolCall using cascading strategies.
   * Never throws on malformed JSON.
   */
  static parseToolCall(output: string): ToolCall | null {
    // Strategy 1: Strict JSON
    try {
      const parsed = JSON.parse(output);
      if (this.isValidToolCall(parsed)) {
        return parsed;
      }
    } catch {
      // Fallback
    }

    // Strategy 2: Partial JSON parsing (find first {} block)
    const blockMatch = output.match(/\{[\s\S]*\}/);
    if (blockMatch) {
      try {
        const parsed = JSON.parse(blockMatch[0]);
        if (this.isValidToolCall(parsed)) {
          return parsed;
        }
      } catch {
        // Fallback
      }
    }

    // Strategy 3: Regex fallback extraction (e.g. action: "tool", action_input: {...})
    const toolMatch = output.match(/"?tool"?\s*:\s*"?([^",\s]+)"?/i) || output.match(/"?action"?\s*:\s*"?([^",\s]+)"?/i);
    if (toolMatch) {
      const tool = toolMatch[1];
      
      // Try to find arguments object
      const argsMatch = output.match(/"?(?:arguments|action_input)"?\s*:\s*(\{[\s\S]*?\})/i);
      if (argsMatch) {
        try {
          // It could be malformed, so we regex the args block or try strict parse
          const args = JSON.parse(argsMatch[1]);
          return { tool, arguments: args };
        } catch {
          // Ultimate fallback: return empty object if args fail to parse
          return { tool, arguments: {} };
        }
      }
      
      return { tool, arguments: {} };
    }

    return null;
  }

  private static isValidToolCall(parsed: any): parsed is ToolCall {
    return parsed && typeof parsed === 'object' && typeof parsed.tool === 'string';
  }
}
