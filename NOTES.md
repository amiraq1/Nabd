# Day 1 Frictions & Notes

*Write down any friction, UI quirk, or architectural limit encountered during real-world usage here. Don't fix it immediately; collect observations to shape v1.1.*

- (Empty for now. Awaiting first real-world session.)
- **Friction 1**: The default model `qwen/qwen3-coder-480b-a35b-instruct` was hardcoded and reached EOL, causing a 410 error on startup. We need to either select a more stable default model, dynamically fetch available models, or handle 410 gracefully by falling back.
- **Friction 2**: The banner in `main.go` was hardcoded to `nabd v0.2` and was not updated with the releases, causing confusion about the running version.
قائمة الموديلات من /models هي كتالوج فقط ولا تعني الصلاحية للاستخدام، والصلاحية والقدرة على استدعاء الأدوات لا تُعرفان إلا بطلب فعلي.
- **Friction 3**: الاستنساخ النظيف في /tmp والبناء منه هو الفحص الوحيد الذي يكشف أعطال غياب الملفات الهامة من المستودع (بسبب أخطاء .gitignore مثل قاعدة ag التي استبعدت مجلد cmd/ag بأكمله). هذا الفحص يستغرق دقيقة واحدة ويجب إجراؤه قبل أي وسم جديد.
- **Friction 5**: The CLI lacks a -version flag, making it hard to verify the currently installed version.
- **Friction 6**: المتغيرات الميتة مثل OPENROUTER_MODEL توحي بأن الإعدادات صحيحة بينما الكود يقرأ NABD_MODEL فقط. بالإضافة إلى ذلك، تثبيت Label المزود يدوياً قد يؤدي إلى ظهور لافتة كاذبة للمستخدم (مثلاً openrouter وهو يكلم خادم آخر)، لذا يجب اشتقاقه من المضيف (Hostname) مباشرة.
- **Friction 7**: كل دفعة نصّ (Chunk) من التدفّق تُطبع في سطر مستقل، مما يؤدي إلى خروج الجواب مقطّعاً عمودياً (سطر جديد لكل دفعة).
- **Friction 8**: ترتيب النصوص وتشوه الحروف العربية (مثل: هل / ًا / أ / . / كيف)، وهذا غالباً ناتج عن قطع دفعات التدفّق (SSE) في منتصف رمز UTF-8 متعدّد البايتات، مما يكسر الحروف ويشوش ترتيب العرض.
