export class ExpandableBuffer {
  private buffer: { id: string; text: string }[] = [];

  push(id: string, fullText: string): void {
    this.buffer.push({ id, text: fullText });
    if (this.buffer.length > 20) {
      this.buffer.shift();
    }
  }

  expandLast(): string | null {
    if (this.buffer.length === 0) return null;
    return this.buffer[this.buffer.length - 1].text;
  }
}

export const globalExpandBuffer = new ExpandableBuffer();
