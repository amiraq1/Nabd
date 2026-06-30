import type { ProcessSession } from './ProcessSession.js';

export class CancellationError extends Error {
  readonly executionId: string;

  constructor(executionId: string, reason: string) {
    super(reason);
    this.name = 'CancellationError';
    this.executionId = executionId;
  }
}

export interface QueueOptions {
  priority?: number;
}

export interface QueueStatistics {
  waiting: number;
  running: number;
  maxConcurrency: number;
  paused: boolean;
}

interface Deferred<T> {
  promise: Promise<T>;
  resolve: (value: T) => void;
  reject: (err: Error) => void;
}

interface WaitingItem {
  executionId: string; // معرف الطابور الموحد والثابت
  factory: () => Promise<ProcessSession | any>; // قبول نتائج مباشرة أو جلسات
  priority: number;
  sequenceNumber: number; // لضمان الـ FIFO الصارم مع تساؤل الأولويات
  enqueuedAt: number;
  deferred: Deferred<ProcessSession | any>;
}

interface RunningItem {
  queueExecutionId: string; // الاحتفاظ بمعرف الطابور الأصلي للربط
  session: ProcessSession | any;
}

export class ExecutionQueue {
  private readonly maxConcurrency: number;
  private readonly waitingItems: WaitingItem[] = [];
  // الخريطة الآن تعتمد على معرف الطابور كمفتاح رئيسي لتوحيد عمليات الإلغاء
  private readonly runningItems: Map<string, RunningItem> = new Map();
  private inflightDispatches = 0;
  private paused = false;
  private nextId = 0;
  private globalSequence = 0; // عداد تسلسلي تصاعدي

  constructor(maxConcurrency: number = 3) {
    if (!Number.isInteger(maxConcurrency) || maxConcurrency < 1) {
      throw new Error(`ExecutionQueue: يجب أن يكون maxConcurrency رقماً صحيحاً موجباً، تم استقبال: ${maxConcurrency}`);
    }
    this.maxConcurrency = maxConcurrency;
  }

  /**
   * إدراج مهمة جديدة في الطابور.
   */
  enqueue(
    factory: () => Promise<ProcessSession | any>,
    options: QueueOptions = {},
  ): Promise<ProcessSession | any> {
    const priority = options.priority ?? 0;
    const executionId = this.generateExecutionId();
    const deferred = this.createDeferred<ProcessSession | any>();

    this.globalSequence += 1;

    const item: WaitingItem = {
      executionId,
      factory,
      priority,
      sequenceNumber: this.globalSequence,
      enqueuedAt: Date.now(),
      deferred,
    };

    this.insertSortedOptimized(item);
    this.drain();

    return deferred.promise;
  }

  /**
   * إلغاء مهمة برقم المعرف الموحد سواء كانت منتظرة أو جارية حالياً.
   */
  cancel(executionId: string): boolean {
    // 1. البحث في قائمة الانتظار
    const waitingIndex = this.waitingItems.findIndex((w) => w.executionId === executionId);
    if (waitingIndex >= 0) {
      const [item] = this.waitingItems.splice(waitingIndex, 1);
      item.deferred.reject(new CancellationError(item.executionId, 'تم إلغاء العملية أثناء وجودها في طابور الانتظار.'));
      return true;
    }

    // 2. البحث في قائمة التنفيذ النشطة باستخدام معرف الطابور الموحد
    const running = this.runningItems.get(executionId);
    if (running !== undefined) {
      // تحقق مما إذا كانت الجلسة قابلة للإلغاء (ProcessSession حقيقي وليس كائن JSON)
      if (typeof running.session?.isTerminal === 'function' && typeof running.session?.cancel === 'function') {
         if (!running.session.isTerminal()) {
           running.session.cancel('SIGTERM');
         }
      }
      return true;
    }

    // 3. محاولة الفحص الاحتياطي بمعرف الجلسة الداخلي في حال تم الاستدعاء به
    for (const [queueId, item] of this.runningItems.entries()) {
      if (item.session?.executionId === executionId) {
        if (typeof item.session?.isTerminal === 'function' && typeof item.session?.cancel === 'function') {
           if (!item.session.isTerminal()) {
             item.session.cancel('SIGTERM');
           }
        }
        return true;
      }
    }

    return false;
  }

  pause(): void {
    this.paused = true;
  }

  resume(): void {
    this.paused = false;
    this.drain();
  }

  clear(): number {
    const count = this.waitingItems.length;
    for (const item of this.waitingItems) {
      item.deferred.reject(new CancellationError(item.executionId, 'تم تفريغ وتنظيف طابور الانتظار بالكامل.'));
    }
    this.waitingItems.length = 0;
    return count;
  }

  running(): any[] {
    return Array.from(this.runningItems.values()).map(item => item.session);
  }

  waiting(): string[] {
    return this.waitingItems.map((w) => w.executionId);
  }

  stats(): QueueStatistics {
    return {
      waiting: this.waitingItems.length,
      running: this.runningItems.size,
      maxConcurrency: this.maxConcurrency,
      paused: this.paused,
    };
  }

  // ---------------- المساعدات الداخلية المحسّنة ----------------

  private generateExecutionId(): string {
    this.nextId += 1;
    return `eq-${Date.now().toString(36)}-${this.nextId.toString(36)}`;
  }

  /**
   * إدراج محسّن بكفاءة خطية O(N) بدلاً من إعادة الفرز الكامل المجهد للمقاييس الخوارزمية
   */
  private insertSortedOptimized(item: WaitingItem): void {
    let insertIndex = this.waitingItems.length;

    for (let i = 0; i < this.waitingItems.length; i++) {
      const current = this.waitingItems[i];
      // الترتيب: الأولوية الأعلى أولاً، وفي حال التساوي، الرقم التسلسلي الأصغر (الأقدم زمنيًا FIFO) أولاً
      if (
        item.priority > current.priority ||
        (item.priority === current.priority && item.sequenceNumber < current.sequenceNumber)
      ) {
        insertIndex = i;
        break;
      }
    }

    this.waitingItems.splice(insertIndex, 0, item);
  }

  private createDeferred<T>(): Deferred<T> {
    let resolveFn!: (value: T) => void;
    let rejectFn!: (err: Error) => void;
    const promise = new Promise<T>((resolve, reject) => {
      resolveFn = resolve;
      rejectFn = reject;
    });
    return { promise, resolve: resolveFn, reject: rejectFn };
  }

  private drain(): void {
    while (
      !this.paused &&
      this.runningItems.size + this.inflightDispatches < this.maxConcurrency
    ) {
      const item = this.waitingItems.shift();
      if (item === undefined) {
        return;
      }
      this.inflightDispatches += 1;
      this.startItem(item);
    }
  }

  private startItem(item: WaitingItem): void {
    item
      .factory()
      .then((session) => {
        this.inflightDispatches -= 1;

        // ربط وحفظ الجلسة باستخدام معرف الطابور الأصلي لمنع التضارب وضمان نجاح الـ cancel
        this.runningItems.set(item.executionId, {
          queueExecutionId: item.executionId,
          session
        });

        item.deferred.resolve(session);
        void this.drainSession(item.executionId, session);
      })
      .catch((err: unknown) => {
        this.inflightDispatches -= 1;
        const error = err instanceof Error ? err : new Error(`فشل مصنع العمليات أثناء البناء: ${String(err)}`);
        item.deferred.reject(error);
        this.drain();
      });
  }

  private async drainSession(queueExecutionId: string, session: ProcessSession | any): Promise<void> {
    try {
      // ✅ التعديل الرئيسي: فحص نوع النتيجة، إذا كانت كائنًا وليس ProcessSession قابل للتدفق، فلا تفعل شيئًا.
      if (session && typeof session.stream === 'function') {
        const stream = session.stream();
        while (true) {
          const result = await stream.next();
          if (result.done === true) {
            break;
          }
        }
      }
    } catch (err: any) {
      // إطلاق تحذير داخلي في السجلات بدلاً من البلع الصامت للأخطاء لتسهيل تتبع أعطال الـ CLI
      console.warn(`[ExecutionQueue Warning] حدث خطأ أثناء تفريغ تدفق البيانات للجلسة ${queueExecutionId}: ${err?.message || err}`);
    } finally {
      this.runningItems.delete(queueExecutionId);
      this.drain();
    }
  }
}
