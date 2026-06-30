import { globalExpandBuffer } from './ExpandableBuffer.js';

export class ThoughtTracker {
  private startTime = 0;

  start(): void {
    this.startTime = Date.now();
  }

  finish(text: string): { elapsedSec: number; preview: string; full: string } {
    const elapsedSec = Math.round((Date.now() - this.startTime) / 1000);
    globalExpandBuffer.push(`thought-${Date.now()}`, text);

    const lines = text.trim().split('\n');
    let preview = lines.find(l => l.trim().length > 0) || '';
    
    // Truncate preview to terminal width if possible (assume 80 if unknown)
    const cols = process.stdout.columns || 80;
    const maxLen = Math.max(10, cols - 10);
    if (preview.length > maxLen) {
      preview = preview.substring(0, maxLen) + '...';
    }

    return { elapsedSec, preview, full: text };
  }

  getLastThought(): string | null {
    return globalExpandBuffer.expandLast();
  }
}
