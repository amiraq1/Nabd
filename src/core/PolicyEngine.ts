import path from 'node:path';
import type { ExecutionPolicy, PolicyViolation } from './types.js';

export interface PolicyValidationContext {
  /** اسم الأمر أو المسار الكامل المطلوب تنفيذه. */
  command: string;
  /** الوسائط الممررة للأمر. */
  args: string[];
  /** المجلد النشط المطلوب للتنفيذ. */
  cwd?: string;
}

const DEFAULT_POLICY: ExecutionPolicy = {
  maxExecutionTimeMs: 45000,
  maxOutputBytes: 20 * 1024 * 1024,
  allowNetwork: true,
  allowFilesystemWrite: true,
  allowDelete: false,
  allowBackgroundProcess: false,
  workingDirectory: process.cwd(),
  environment: { ...process.env },
  allowedCommands: [], // فارغة تعني منع كل شيء افتراضياً ما لم يتم تحديد الاستثناء
};

const MAX_EXECUTION_TIME_MS = 600000; // 10 دقائق كحد أقصى
const MAX_OUTPUT_BYTES = 100 * 1024 * 1024; // 100 ميجابايت

/** قائمة سوداء بمتغيرات البيئة الخطيرة لمنع حقن المكتبات (Library Injection) */
const DANGEROUS_ENV_KEYS = ['LD_PRELOAD', 'LD_LIBRARY_PATH', 'PYTHONPATH', 'NODE_OPTIONS'];

export class PolicyEngine {
  private readonly defaultPolicy: ExecutionPolicy;

  constructor(customDefaultPolicy?: Partial<ExecutionPolicy>) {
    // حل مشكلة الترتيب في الباني عبر بناء السياسة مباشرة من الثابت الافتراضي أولاً
    this.defaultPolicy = {
      ...DEFAULT_POLICY,
      ...customDefaultPolicy,
      environment: { ...DEFAULT_POLICY.environment, ...(customDefaultPolicy?.environment ?? {}) },
    };
  }

  /**
   * للتحقق من طلب التنفيذ مقابل السياسة المعتمدة.
   */
  validate(policy: ExecutionPolicy, context: PolicyValidationContext): PolicyViolation[] {
    const violations: PolicyViolation[] = [];

    // 1. التحقق من الحدود الزمنية
    if (!Number.isInteger(policy.maxExecutionTimeMs) || policy.maxExecutionTimeMs <= 0) {
      violations.push({
        rule: 'maxExecutionTimeMs',
        message: `يجب أن يكون الحد الأقصى لوقت التنفيذ رقماً صحيحاً موجباً (تم استقبال: ${policy.maxExecutionTimeMs}).`,
      });
    } else if (policy.maxExecutionTimeMs > MAX_EXECUTION_TIME_MS) {
      violations.push({
        rule: 'maxExecutionTimeMs',
        message: `تجاوز الحد الأقصى المسموح به للنظام وهو ${MAX_EXECUTION_TIME_MS} مللي ثانية.`,
      });
    }

    // 2. التحقق من حجم المخرجات
    if (!Number.isInteger(policy.maxOutputBytes) || policy.maxOutputBytes <= 0) {
      violations.push({
        rule: 'maxOutputBytes',
        message: `يجب أن يكون الحد الأقصى للمخرجات رقماً صحيحاً موجباً (تم استقبال: ${policy.maxOutputBytes}).`,
      });
    } else if (policy.maxOutputBytes > MAX_OUTPUT_BYTES) {
      violations.push({
        rule: 'maxOutputBytes',
        message: `تجاوز الحد الأقصى لحجم المخرجات المسموح به للنظام وهو ${MAX_OUTPUT_BYTES} بايت.`,
      });
    }

    // 3. التحقق من المجلد النشط (Context CWD Validation)
    if (typeof policy.workingDirectory !== 'string' || policy.workingDirectory.length === 0) {
      violations.push({
        rule: 'workingDirectory',
        message: 'مسار مجلد العمل (workingDirectory) في السياسة لا يمكن أن يكون فارغاً.',
      });
    }

    if (context.cwd) {
      const resolvedContextCwd = path.resolve(context.cwd);
      const resolvedPolicyCwd = path.resolve(policy.workingDirectory);
      
      // التأكد من أن المجلد المطلوب لا يخرج عن نطاق المجلد المسموح (منع الـ Path Traversal)
      if (!resolvedContextCwd.startsWith(resolvedPolicyCwd)) {
        violations.push({
          rule: 'workingDirectory',
          message: `المجلد النشط المطلوب (${resolvedContextCwd}) يقع خارج نطاق مجلد العمل المسموح به (${resolvedPolicyCwd}).`,
        });
      }
    }

    // 4. التحقق الصارم من الأوامر المسموحة (Strict Whitelisting)
    const allowed = policy.allowedCommands;
    const requested = context.command;

    if (!Array.isArray(allowed) || allowed.length === 0) {
      violations.push({
        rule: 'allowedCommands',
        message: `تم رفض الأمر "${requested}". قائمة الأوامر المسموح بها فارغة (Default Deny).`,
      });
    } else {
      // التحقق الصارم: يجب أن يتطابق المسار بالكامل أو يتطابق الأمر إذا كان أمراً نظامياً مباشراً بدون تلاعب بالمسارات
      const isMatched = allowed.some((entry) => {
        if (typeof entry !== 'string' || entry.length === 0) return false;
        
        // إذا تم تحديد مسار مطلق في السياسة، نقوم بحل المسارات ومقارنتها بشكل صارم
        if (path.isAbsolute(entry) || entry.startsWith('.')) {
          return path.resolve(entry) === path.resolve(requested);
        }
        
        // إذا كان اسم أمر مباشر مثل (git) والأمر المطلوب يحاول استدعاء مسار محلي لنفس الاسم، نرفضه
        if (entry === requested) {
          return true;
        }
        
        return false;
      });

      if (!isMatched) {
        violations.push({
          rule: 'allowedCommands',
          message: `الأمر "${requested}" غير مدرج في قائمة الأوامر المصرح بها [${allowed.join(', ')}].`,
        });
      }
    }

    // 5. فحص حقن البيئة الخطيرة
    if (policy.environment) {
      for (const key of DANGEROUS_ENV_KEYS) {
        if (policy.environment[key] !== undefined) {
          violations.push({
            rule: 'environment',
            message: `محاولة محظورة لحقن متغير البيئة الحساس والخطير: (${key}).`,
          });
        }
      }
    }

    return violations;
  }

  /**
   * دمج السياسة الخاصة بالأداة مع السياسة الافتراضية للنظام.
   */
  mergePolicy(toolPolicy: Partial<ExecutionPolicy>): ExecutionPolicy {
    const mergedEnvironment = {
      ...this.defaultPolicy.environment,
      ...(toolPolicy.environment ?? {}),
    };

    return {
      maxExecutionTimeMs: toolPolicy.maxExecutionTimeMs ?? this.defaultPolicy.maxExecutionTimeMs,
      maxOutputBytes: toolPolicy.maxOutputBytes ?? this.defaultPolicy.maxOutputBytes,
      allowNetwork: toolPolicy.allowNetwork ?? this.defaultPolicy.allowNetwork,
      allowFilesystemWrite: toolPolicy.allowFilesystemWrite ?? this.defaultPolicy.allowFilesystemWrite,
      allowDelete: toolPolicy.allowDelete ?? this.defaultPolicy.allowDelete,
      allowBackgroundProcess: toolPolicy.allowBackgroundProcess ?? this.defaultPolicy.allowBackgroundProcess,
      workingDirectory: toolPolicy.workingDirectory ?? this.defaultPolicy.workingDirectory,
      environment: mergedEnvironment,
      allowedCommands: toolPolicy.allowedCommands ?? this.defaultPolicy.allowedCommands,
    };
  }
}

export const policyEngine = new PolicyEngine();
