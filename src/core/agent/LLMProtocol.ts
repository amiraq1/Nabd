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
    return { kind: 'final_answer', text: output.trim() };
  }

  /**
   * استخراج ذكي لـ ToolCall مع طبقة تعقيم مسبقة لحماية النظام من هلوسة تنسيق الـ Markdown
   */
  static parseToolCall(output: string): ToolCall | null {
    // 0. Sanitization: تنظيف علامات الماركداون التي تعشق النماذج إضافتها
    const cleanOutput = output
      .replace(/```(?:json)?/gi, '')
      .replace(/```/g, '')
      .trim();

    // Strategy 1: Strict JSON
    try {
      const parsed = JSON.parse(cleanOutput);
      if (this.isValidToolCall(parsed)) return parsed;
    } catch { /* Fallback */ }

    // Strategy 2: Partial JSON parsing (البحث عن أول كائن {} آمن خوارزمياً)
    const blockMatch = cleanOutput.match(/\{[\s\S]*?\}/);
    if (blockMatch) {
      try {
        const parsed = JSON.parse(blockMatch[0]);
        if (this.isValidToolCall(parsed)) return parsed;
      } catch { /* Fallback */ }
    }

    // Strategy 3: Regex Regex fallback extraction (أسرع وأقل استهلاكاً للمعالج)
    const toolMatch = cleanOutput.match(/"?tool"?\s*:\s*"?([^",\s}]+)"?/i) || cleanOutput.match(/"?action"?\s*:\s*"?([^",\s}]+)"?/i);
    if (toolMatch) {
      const tool = toolMatch[1];
      const argsMatch = cleanOutput.match(/"?(?:arguments|action_input)"?\s*:\s*(\{[\s\S]*?\})/i);
      if (argsMatch) {
        try {
          const args = JSON.parse(argsMatch[1]);
          return { tool, arguments: args };
        } catch {
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
