import type { SystemEvent } from './ExecutionEvent.js';

type Listener<T = SystemEvent> = (event: T) => void;

export class EventBus<T = SystemEvent> {
  private readonly listeners = new Set<Listener<T>>();

  subscribe(listener: Listener<T>): void {
    this.listeners.add(listener);
  }

  unsubscribe(listener: Listener<T>): void {
    this.listeners.delete(listener);
  }

  once(listener: Listener<T>): void {
    const wrapper = (event: T) => {
      this.unsubscribe(wrapper);
      listener(event);
    };
    this.subscribe(wrapper);
  }

  emit(event: T): void {
    // Iterate over a snapshot to allow listeners to unsubscribe during emit safely
    for (const listener of Array.from(this.listeners)) {
      listener(event);
    }
  }

  listenerCount(): number {
    return this.listeners.size;
  }

  clear(): void {
    this.listeners.clear();
  }
}

export const globalEventBus = new EventBus();
