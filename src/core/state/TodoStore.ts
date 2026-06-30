import { randomUUID } from 'node:crypto';

export interface TodoItem {
  id: string;
  text: string;
  done: boolean;
}

export class TodoStoreManager {
  private stores = new Map<string, TodoItem[]>();
  // تحديد سقف للجلسات المحفوظة لمنع تسرب الذاكرة (Memory Leak Prevention)
  private readonly MAX_SESSIONS = 10;

  private getStore(sessionId: string): TodoItem[] {
    if (!this.stores.has(sessionId)) {
      this.enforceSessionLimit();
      this.stores.set(sessionId, []);
    }
    return this.stores.get(sessionId)!;
  }

  /**
   * إزالة الجلسات الأقدم (FIFO) عند تجاوز الحد الأقصى
   */
  private enforceSessionLimit(): void {
    if (this.stores.size >= this.MAX_SESSIONS) {
      // الـ Map في جافاسكريبت يحافظ على ترتيب الإدخال، لذا العنصر الأول هو الأقدم
      const oldestSessionId = this.stores.keys().next().value;
      if (oldestSessionId) {
        this.stores.delete(oldestSessionId);
      }
    }
  }

  setAll(sessionId: string, items: string[]): void {
    const todos = items.map((text) => ({
      id: `todo-${randomUUID()}`,
      text,
      done: false
    }));
    this.stores.set(sessionId, todos);
  }

  markDone(sessionId: string, index: number): boolean {
    const store = this.getStore(sessionId);
    if (index < 0 || index >= store.length) return false;
    store[index].done = true;
    return true;
  }

  markPending(sessionId: string, index: number): boolean {
    const store = this.getStore(sessionId);
    if (index < 0 || index >= store.length) return false;
    store[index].done = false;
    return true;
  }

  getAll(sessionId: string): TodoItem[] {
    return this.getStore(sessionId);
  }

  clear(sessionId: string): void {
    this.stores.delete(sessionId);
  }
}

export const todoStore = new TodoStoreManager();
