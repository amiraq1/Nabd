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
- **Friction 9**: التنفيذ الحرفي للتعليمات المكانية (مثل "أضف في أعلى الملف") يتم دون فهم للعرف اللغوي، مما قد يفسد مثلاً وثيقة الحزمة (Package Doc) في Go إذا وُضع الكود أو التعليق فوقها مباشرة بدلاً من تحتها.
- **P0-1**: `tool_start` يُبَثّ في `streamTurn` قبل البوّابة، ويشطره `turn_end`؛ الإغلاق حين لا يظهر `tool_start` لأداة مرفوضة، وحين تقع دورة الأداة كاملة بعد `turn_end`.
- **P0-1.5**: سباق (Race condition) في `emitAt` أدى إلى تشوه العرض وتأخير تسلسل الأحداث المبعوثة للقناة. تم الإصلاح بنقل `Unlock` إلى ما بعد الإرسال بـ `defer`. ملاحظة هامة: `Sink.Emit` قد تنتظر حتى ثانيتين إذا امتلأت القناة، وهذا الانتظار أصبح الآن تحت القفل، وهو مقبول لسعة القناة (128)، لكن يمنع منعاً باتاً استدعاء أي مصرّف لدالة تعود لـ `Loop` تطلب القفل نفسه لتجنب الجمود (Deadlock). بالإضافة إلى ذلك، يوجد تعليق لـ `Fanout` في `loop.go` يصفها كدالة، بينما هي مجرد نوع، مما يجعله تعليقاً يتيماً.
- **P0-1.6**: سباق آخر في طبقة العرض (`Bubbletea`) حيث `tea.Batch` كان يُشغّل كل `tea.Println` لدلتا النص في Goroutine منفصلة، مما سبّب تشوّش ترتيب الكلمات والطباعة العمودية. تم الإصلاح بتجميع `TextDelta` في مخزن (`buf`) في نموذج `Chat` وطباعتها دفعة واحدة عند أي حدث فاصل (`TurnEnd`, `ToolStart`, `Interrupted`, الخ).
- **P0-1.6 closure (OBSERVED)**: The display-layer race was two `tea.Println` commands racing inside one `tea.Batch` (the flushed text buffer and the rendered event). Fixed by emitting a single `tea.Println` per Update: the pieces are joined with `\n` in intended order. This is distinct from P0-1.5 (journal layer, emitAt mutex).
- **Friction 7 closure (OBSERVED)**: One line per delta is closed by the `m.buf` accumulator in `Chat` — text deltas accumulate and flush once on any non-delta event, `RunEnd`, `Interrupted`, or `doneMsg`.
- **Operational (OBSERVED)**: `-race` is unsupported on android/arm64; deterministic race verification requires linux/amd64.
- **Operational (OBSERVED)**: TPM ceiling is 8000 tokens on the current Groq key, so any file over ~200 lines cannot be read in one call. `read_file` needs a byte limit plus offset/limit (v1.1).
- **Limitation (OBSERVED)**: The edit log lives in memory only and dies with the process, so `/undo` does not survive restart and `--continue` does not restore it. This belongs in the README "Limits" section too.
