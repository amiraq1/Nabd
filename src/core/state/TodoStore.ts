export interface TodoItem {
  id: string;
  text: string;
  done: boolean;
}

export class TodoStoreManager {
  private stores = new Map<string, TodoItem[]>();

  private getStore(sessionId: string): TodoItem[] {
    if (!this.stores.has(sessionId)) {
      this.stores.set(sessionId, []);
    }
    return this.stores.get(sessionId)!;
  }

  setAll(sessionId: string, items: string[]): void {
    const todos = items.map((text, idx) => ({
      id: `todo-${Date.now()}-${idx}`,
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
