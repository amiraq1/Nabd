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
    // التقاط لقطة (Snapshot) للمستمعين لضمان الأمان أثناء تعديل القائمة
    const snapshot = Array.from(this.listeners);
    
    for (const listener of snapshot) {
      try {
        // عزل الأخطاء (Fault Isolation): خطأ مستمع واحد لن يسقط الناقل
        listener(event);
      } catch (err) {
        // تسجيل الخطأ دون كسر تدفق الأحداث الحرج للنظام
        console.warn(`[EventBus] خطأ في أحد مستمعي الأحداث أثناء معالجة الحدث:`, err);
      }
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
