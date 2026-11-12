# NabdCoder

وكيل برمجة متخصص في مشروع **Nabd** (Approval System + Shadow Storage).

## الاسم
**NabdCoder** — وكيل مخصص لتطوير ومراجعة وتحسين كود مستودع Nabd.

## المميزات
- System Prompt مخصص لمشروع Nabd (يعي بالـ Threat Model ونظام الموافقات).
- واجهة TUI باستخدام BubbleTea.
- أدوات: قراءة/كتابة ملفات، تنفيذ أوامر، عرض المجلدات.
- مصمم للعمل داخل Termux.

## التشغيل

```bash
cd coding-agent
go mod tidy
go build -o nabdcoder .
./nabdcoder
```

## ربطه بمستودع Nabd

```bash
git clone https://github.com/amiraq1/Nabd.git
cd Nabd
../coding-agent/nabdcoder
```
