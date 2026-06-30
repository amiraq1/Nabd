/**
 * قائمة محدثة وصارمة من التعبيرات النمطية لحماية بيئة Termux / Linux
 * من الأوامر التدميرية، مع إغلاق ثغرات التلاعب بالمسافات والدمج.
 */
export const DESTRUCTIVE_PATTERNS: RegExp[] = [
  // يمنع أي محاولة حذف متكرر أو قسري (rm -rf, rm -fr, rm -r -f) مع حدود الكلمة
  /\brm\b\s+(?:-[a-zA-Z]*r[a-zA-Z]*f[a-zA-Z]*|-[a-zA-Z]*f[a-zA-Z]*r[a-zA-Z]*|-[rf]\s+-[rf])/i,
  
  // يمنع حذف المجلدات الجذرية أو استخدام الـ Wildcards في مسارات غير محددة
  /\brm\b\s+-[rR]\s+(?:\/|\*|\.\/)/i,
  
  // حماية أدوات فرمتة الأقراص والمسح العميق
  /\bmkfs\b/i,
  /\bdd\b\s+.*?(?:of=\/dev|of=\/system|of=\/data)/i,
  
  // منع إعادة التوجيه إلى أجهزة النظام الحساسة
  />\s*\/dev\/(?:sd|block|kmsg)/i,
  
  // منع رفع ملفات الجيت بالقوة (حماية السورس كود)
  /\bgit\b\s+push\s+(?:--force|-f)\b/i,
  
  // منع تغيير صلاحيات الملفات بشكل كارثي
  /\bchmod\b\s+(?:-R\s+)?(?:777|a\+rw)/i,
  
  // حماية بيئة Termux وأندرويد من تصعيد الصلاحيات (Privilege Escalation)
  /\b(?:sudo|su|tsu)\b/i,
  
  // منع التعديل على ملفات النظام أو جذور Termux
  />\s*\/etc\/(?:passwd|shadow)/i,
  />\s*\/data\/data\/com\.termux/i
];

export function isDestructive(command: string): boolean {
  if (!command || command.trim() === '') return false;
  
  // تنظيف الأمر من الهروب الخلفي (Backslash Escaping) الذي قد يخدع التعبيرات
  // مثال: r\m -rf لن تمر من هذا الفحص بعد التنظيف
  const sanitizedCommand = command.replace(/\\/g, '');
  
  return DESTRUCTIVE_PATTERNS.some(pattern => pattern.test(sanitizedCommand));
}
