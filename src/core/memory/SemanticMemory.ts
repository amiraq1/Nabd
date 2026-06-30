import fs from 'node:fs';
import fsp from 'node:fs/promises';
import path from 'node:path';
import { randomUUID } from 'node:crypto';

export interface MemoryEntry {
  id: string;
  timestamp: number;
  tags: string[];
  content: string;
}

export class SemanticMemory {
  private readonly dbPath: string;
  private readonly tempDbPath: string;
  private entries: MemoryEntry[] = [];
  
  // سقف الذاكرة لمنع انهيار تطبيق Node.js واستنزاف الـ RAM
  private readonly MAX_ENTRIES = 2000;
  
  // مؤشرات للتحكم في عمليات الحفظ الخلفية (Background Saving)
  private saveTimeout: NodeJS.Timeout | null = null;
  private isSaving = false;
  private needsSave = false;

  constructor(dbPath?: string) {
    this.dbPath = dbPath || path.join(process.cwd(), '.nabd_memory.json');
    this.tempDbPath = `${this.dbPath}.tmp`;
    this.loadSync(); // التحميل المتزامن مقبول فقط عند الإقلاع
  }

  /**
   * تحميل الذاكرة عند بدء التشغيل مع حماية ضد الملفات المشوهة.
   */
  private loadSync(): void {
    try {
      if (fs.existsSync(this.dbPath)) {
        const data = fs.readFileSync(this.dbPath, 'utf8');
        this.entries = JSON.parse(data);
      }
    } catch (error) {
      console.warn(`[SemanticMemory] تحذير: فشل قراءة الذاكرة من ${this.dbPath}. تم بدء ذاكرة جديدة.`, error);
      this.entries = [];
    }
  }

  /**
   * حفظ مجدول وغير متزامن (Debounced Async Save).
   * يمنع تجميد الـ Event Loop ويقلل إهلاك ذاكرة الفلاش في الهاتف.
   */
  private scheduleSave(): void {
    if (this.saveTimeout) {
      clearTimeout(this.saveTimeout);
    }
    this.needsSave = true;
    
    // تأخير الحفظ لمدة ثانيتين لتجميع الكتابات المتتالية (Batching)
    this.saveTimeout = setTimeout(() => {
      void this.executeAtomicSave();
    }, 2000);
  }

  /**
   * كتابة ذرية (Atomic Write) لحماية ملف الـ JSON من الفساد عند الانقطاع المفاجئ.
   */
  private async executeAtomicSave(): Promise<void> {
    if (this.isSaving) {
      // إذا كان هناك حفظ جاري، أعد جدولة الحفظ الحالي
      this.scheduleSave();
      return;
    }

    this.isSaving = true;
    this.needsSave = false;

    try {
      const data = JSON.stringify(this.entries, null, 2);
      // 1. الكتابة في ملف مؤقت
      await fsp.writeFile(this.tempDbPath, data, 'utf8');
      // 2. استبدال الملف الأصلي دفعة واحدة (عملية ذرية في أنظمة Unix/Linux)
      await fsp.rename(this.tempDbPath, this.dbPath);
    } catch (error) {
      console.error(`[SemanticMemory] خطأ حرج أثناء حفظ الذاكرة:`, error);
      this.needsSave = true; // إعادة المحاولة لاحقاً
    } finally {
      this.isSaving = false;
      if (this.needsSave) {
        this.scheduleSave();
      }
    }
  }

  /**
   * تقليم الذاكرة (Pruning) للحفاظ على السعة دون تجاوز الحد الأقصى.
   */
  private enforceMemoryLimit(): void {
    if (this.entries.length > this.MAX_ENTRIES) {
      // ترتيب زمني تصاعدي (الأقدم أولاً) ثم حذف الأقدم للحفاظ على حجم المصفوفة
      this.entries.sort((a, b) => a.timestamp - b.timestamp);
      const excess = this.entries.length - this.MAX_ENTRIES;
      this.entries.splice(0, excess);
    }
  }

  public forceSave(): void {
    if (this.saveTimeout) clearTimeout(this.saveTimeout);
    // تنفيذ مباشر للحفظ (مفيد عند إغلاق التطبيق Graceful Shutdown)
    void this.executeAtomicSave();
  }

  public remember(content: string, tags: string[] = []): string {
    const entry: MemoryEntry = {
      id: randomUUID(),
      timestamp: Date.now(),
      tags,
      content,
    };
    
    this.entries.push(entry);
    this.enforceMemoryLimit();
    this.scheduleSave();
    
    return entry.id;
  }

  public recall(query: string, limit: number = 5): MemoryEntry[] {
    if (!query || query.trim() === '') return [];

    const terms = query.toLowerCase().split(/\s+/);

    // تحسين خوارزمية البحث (تقليل الـ Object allocations)
    const scored = this.entries.map(entry => {
      let score = 0;
      const contentLower = entry.content.toLowerCase();
      // الانضمام كـ String أسرع من الـ Array.includes في الحلقات المتكررة
      const tagsString = entry.tags.join(' ').toLowerCase();

      for (const term of terms) {
        if (contentLower.includes(term)) score += 1;
        if (tagsString.includes(term)) score += 2;
      }
      return { entry, score };
    });

    return scored
      .filter(s => s.score > 0)
      .sort((a, b) => b.score - a.score || b.entry.timestamp - a.entry.timestamp)
      .slice(0, limit)
      .map(s => s.entry);
  }

  public clear(): void {
    this.entries = [];
    this.scheduleSave();
  }
}

export const semanticMemory = new SemanticMemory();
