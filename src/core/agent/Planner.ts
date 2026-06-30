import { LLMProtocol, type ToolCall } from './LLMProtocol.js';

export interface PlannerConfig {
  maxIterations: number;
  maxRepeatedCalls?: number; 
  initialIterations?: number;
  maxConsecutiveErrors?: number; // إضافة سقف مخصص لأخطاء التنسيق
}

export type PlannerDecision =
  | { action: 'CONTINUE'; call: ToolCall }
  | { action: 'FINAL_ANSWER'; text: string }
  | { action: 'STOP'; reason: string }
  | { action: 'RETRY_ERROR'; error: string };

export class Planner {
  private iterations = 0;
  private consecutiveErrors = 0;
  private lastCallSignature: string | null = null;
  private repeatedCallCount = 0;
  private readonly maxConsecutiveErrors: number;

  constructor(
    private config: PlannerConfig = { maxIterations: 30, maxRepeatedCalls: 2, maxConsecutiveErrors: 3 }
  ) {
    this.iterations = config.initialIterations ?? 0;
    this.maxConsecutiveErrors = config.maxConsecutiveErrors ?? 3;
  }

  /**
   * توليد بصمة سريعة وخفيفة على الذاكرة والمعالج بدلاً من التشفير الثقيل (SHA-256)
   */
  private generateSignature(call: ToolCall): string {
    return `${call.tool}::${JSON.stringify(call.arguments || {})}`;
  }

  decide(llmOutput: string): PlannerDecision {
    // 1. التحقق من الحدود القصوى للمحاولات
    if (this.iterations >= this.config.maxIterations) {
      return { action: 'STOP', reason: 'تم الوصول للحد الأقصى من المحاولات المسموحة (Max Iterations).' };
    }

    // 2. التحقق من تجاوز أخطاء التنسيق
    if (this.consecutiveErrors >= this.maxConsecutiveErrors) {
      return { action: 'STOP', reason: 'تم الإيقاف القسري: النموذج مستمر في توليد تنسيق JSON مشوه ولا يستجيب للتصحيح.' };
    }

    this.iterations++;

    let parsed;
    try {
      // محاولة تحليل المخرجات بأمان
      parsed = LLMProtocol.parse(llmOutput);
    } catch (error: any) {
      // التقاط الخطأ، زيادة العداد، وتوجيه الوكيل لتصحيح نفسه
      this.consecutiveErrors++;
      return { 
        action: 'RETRY_ERROR', 
        error: error.message || 'تنسيق الاستجابة غير صالح. يجب استخدام صيغة JSON الصارمة.' 
      };
    }

    // إذا كانت الإجابة نهائية
    if (parsed.kind === 'final_answer') {
      return { action: 'FINAL_ANSWER', text: parsed.text };
    }

    // إذا كان طلب أداة (Tool Call)
    const call = parsed.call;
    const callSignature = this.generateSignature(call);

    // 3. كشف الحلقات المفرغة (Ghost Call Detection)
    if (callSignature === this.lastCallSignature) {
      this.repeatedCallCount++;
      
      if (this.repeatedCallCount >= (this.config.maxRepeatedCalls ?? 2)) {
        // بدلاً من القتل الفوري، نعتبر التكرار خطأ منطقي ونطلب من الذكاء الاصطناعي تغيير خطته
        this.consecutiveErrors++;
        return { 
          action: 'RETRY_ERROR', 
          error: `[تحذير النظام] أنت تقوم بتكرار استدعاء الأداة '${call.tool}' بنفس الوسائط تماماً. يبدو أنك عالق في حلقة. يرجى مراجعة السياق واختيار مسار أو أداة مختلفة.` 
        };
      }
    } else {
      // إعادة تعيين العدادات عند استخدام أداة جديدة أو وسائط مختلفة
      this.repeatedCallCount = 0;
      this.lastCallSignature = callSignature;
    }

    // تصفير أخطاء التنسيق عند النجاح في اتخاذ قرار سليم
    this.consecutiveErrors = 0;
    return { action: 'CONTINUE', call };
  }

  recordToolSuccess(): void {
    // نجاح الأداة يعني أن الخطة تسير بشكل جيد
    this.consecutiveErrors = 0;
  }

  recordToolFailure(): void {
    // فشل الأداة لا يعني فشل التنسيق، لذلك لا نزيد consecutiveErrors هنا.
    // إذا كرر الوكيل استدعاء الأداة الفاشلة، سيتولى "Ghost Call Detection" معاقبته.
  }

  getIterations(): number {
    return this.iterations;
  }
}
