# nabd — نبض

وكيل برمجة طرفي يُكتب ويُشغّل من هاتف: ملف Go تنفيذي واحد، سجل جلسة JSONL قابل للتدقيق، أذونات افتراضية بالرفض، وتراجع مستقل عن Git.

**A terminal coding agent built on a phone.** One Go binary, an append-only event journal, default-deny permissions, and git-independent undo.

> Security and containment claims live in [docs/THREAT_MODEL.md](docs/THREAT_MODEL.md), not in this README.

The installable binary is `nabd`; the package path remains `./cmd/ag`.

## Releases

| Release | Status |
|---|---|
| `v1.4.0` | Published with binaries and `checksums.txt` |
| `v1.3.0` | Published with binaries and `checksums.txt` |

Download assets from [GitHub Releases](https://github.com/amiraq1/Nabd/releases). Verify a downloaded release in the directory containing its assets:

```sh
sha256sum -c checksums.txt
```

See [docs/RELEASING.md](docs/RELEASING.md) for the release process.

## Build and run

```sh
go build -o nabd ./cmd/ag
# or: ./build.sh

./nabd                            # new conversation in the current directory
./nabd --continue                 # resume the latest session
./nabd --replay <file.jsonl>      # replay a session; --speed 0 is instant
./nabd -feed                      # experimental full-screen UI
./nabd --version                  # version · commit · date

./nabd -p "task text"             # headless answer on stdout
./nabd -p - < file                # task from stdin
./nabd -p "..." --json            # journal JSONL on stdout
./nabd -p "..." --max-turns 8
./nabd -p "count the go files" --permission-mode allow-reads
```

## Configuration

Config v1 uses `NABD_CONFIG` or `~/.ag/config`:

```sh
mkdir -p ~/.ag && touch ~/.ag/config && chmod 600 ~/.ag/config
cat >> ~/.ag/config <<'EOF'
 backup/config-conflict-notice-pre-rebase
ANTHROPIC_API_KEY=sk-ant-...
NABD_MODEL=claude-sonnet-4-5
EOF
```

الملف `KEY=VALUE` سطرًا سطرًا، يتسامح مع `export` والاقتباس و`#` للتعليق.
ما في الملف يغلب ما في البيئة. ملفٌ مقروء للغير يُرفض عند التشغيل ولا يُقرأ.
`NABD_CONFIG` يغيّر موقعه.

**الأسبقية.** `~/.ag/config` يفوز على متغيّرات البيئة عند التعارض — وهذا مقصود:
الملف مصدر حقيقة واحد محميّ بصلاحية `600`، ولا يُطغى عليه `export` قديم في
`.bashrc`. تبقى متغيّرات البيئة مدخلات احتياطية (fallback) للمفاتيح غير المحددة
في الملف وفق القواعد المعمول بها. التجاوز اللحظي لجلسة واحدة يكون بتوجيه `NABD_CONFIG`
إلى ملف آخر بصلاحية `600`، لا بتصدير المتغيّر. عند التعارض يطبع البرنامج إشعارًا
يسمّي المفاتيح المتجاهَلة بأسمائها فقط، دون قيمها، ولا يصدر إشعار إذا كانت القيم
متطابقة أو فارغة.

متغيّرات اختيارية (في الملف أو البيئة): `NABD_PROVIDER` لفرض مزوّد،
`NABD_MODEL` لاسم الموديل، `NABD_BASE_URL` لخادم متوافق، `NABD_CTX` لحجم
نافذة السياق، `NABD_MAX_TOKENS` لسقف الردّ الواحد (افتراضيًا ١٠٢٤، بين ١٢٨ و٨١٩٢ — المزوّدون
الذين يحسبون الرموز بالدقيقة يخصمون هذا الرقم قبل التوليد، فالرقم الكبير
يخنق القراءة).

داخل المحادثة: `/undo [n]` و `/edits` و `/rewind [n]` و `/ctx` و `/compact` و `/help`.
عند سؤال الإذن: `y` مرّة واحدة، `a` لبقية الجلسة، `n` رفض. `ctrl+c` يوقف الدور،
`ctrl+d` يخرج.

## المعمار في ستّ فقرات

**الحدث هو العقد.** كل ما يجري — رسالة، جزء نصّ، نداء أداة، سؤال إذن، خطأ،
مقاطعة — حدثٌ بحقول مسطّحة يُلحق بملف `session.jsonl` ولا يُعدَّل بعدها.
الواجهة تتغيّر كل يوم؛ هذا الملف لا.

**الملف شجرة لا قائمة.** كل حدث يحمل `parent`، فالسجلّ المُلحَق فقط يصف شجرة.
`/rewind` لا يحذف شيئًا: يُلحق حدثًا يشير أبوه إلى ما قبل الدور المقصوص،
فيصبح الفرع بعده غير قابل للوصول من `Live()` وباقيًا على القرص إلى الأبد.
القصّ إضافة، لا حذف.

**الرفض هو القيمة الصفرية.** `Decision(0) == Deny`. إذن مفقود، أو حقل غائب من
JSON، أو نصّ غير معروف — كلّها تُقرأ رفضًا، ويحرس ذلك اختبار مستقلّ. هذا القرار
جاء من قراءة وكيل آخر ينهار إلى السماح عند الغموض.

**الاحتواء بوّابة واحدة.** `Root.Resolve` هي الدالة الوحيدة المسموح لها بقبول
مسار: تحلّ أعمق سلف موجود بـ `EvalSymlinks`، تُلحق الذيل الناقص، ثم تقارن بـ
`filepath.Rel` — بهذا الترتيب، لأن التنظيف قبل الحلّ هو الثغرة الكلاسيكية،
والمجلد الرمزي بذيل غير موجود هو الثغرة الخبيثة. اثنتا عشرة صورة هروب مُختبَرة.

**التراجع مستقلّ عن git.** قبل كل كتابة تُلتقط الحالة السابقة بـ SHA-256
في مخزن الظلّ `.ag/shadow` (مُعتمد على المحتوى، بمعرّف من الشكل
`s256:<64 رقمًا ست عشريًا صغيرًا>`). لا يُعتمد على `git gc` أو كائنات git لإبقاء
الكائنات قابلة للاسترجاع. الكتابة ذرّية عبر ملف مؤقّت ثم `rename`، ثم يُعاد
القراءة ويُقارن الهاش: كتابة لا يمكن إثباتها لم تحدث. و`/undo` يرفض إن تغيّر
الملف بعد أن كتبه الوكيل — أن يرفض التراجع أهون من أن يدهس عملك.

**الضغط قيدٌ لا مقصّ.** حين يقارب السياق ثلاثة أرباع النافذة تُلحق إدخالة
`compact` تحمل `first_kept` وملخّصًا؛ لا يُعاد كتابة شيء. الحدّ يقع على رسالة
مستخدم حصرًا، لأن القطع داخل دور يترك `tool_result` بلا `tool_use` فيُرفض
الطلب التالي كليًا. وقبل الملخّص الغالي يمرّ مقصّ رخيص يُفرغ مخرجات الأدوات
القديمة ويُبقي الأخطاء — الخطأ قصير ويمنع التكرار.

## الحدود — اقرأها قبل أن تثق

هذه ليست فقرة تواضع. هي ما تحتاج معرفته قبل أن تعطي الأداة مجلدًا يهمّك.

**`bash` تخرج من الصندوق.** أداة `bash` تعمل في مجلد المشروع لكنها لا تمرّ
بـ `Resolve` ولا يمكنها أن تمرّ به. `cd ..` تعمل. `rm -rf ~/x` تعمل. `snap` لا
ترى شيئًا من ذلك، فـ `/undo` **لا يغطّي أي أمر صدفة**. الحماية الوحيدة هناك
عينك عند سؤال الإذن، ولهذا `bash` مصنّفة `Executing` ولا تقبل تصريح جلسة
مهما ضغطت `a`.

**بيئة الأمر تُبنى من قائمة مسموحة.** أداة `bash` لا ترث بيئة العملية
العالمية أبدًا — يبدأ بناء البيئة من شريحة فارغة وتُنسخ فقط أسماء مُصرَّح بها
صراحةً (PATH, TERM, LANG, LC_*, TMPDIR/TMP/TEMP) بعد تطهيرها، وبترتيب
حتميّ. كل ما عداها غائب عن بيئة الصدفة بالإنشاء — بما في ذلك كلّ متغيّرِ
سرّ، و`BASH_ENV`، و`ENV`، ومتجهيات حقن المكتبات. يُستبدل `HOME` بمجلد مؤقت
`0700` يُحذف بعد كل استدعاء. المفتاح يُقرأ من `~/.ag/config` أوّلًا: ملف خارج
الجذر لا يكتبه البرنامج أبدًا، يُرفض إن كان مفتوحًا للغير أو رابطًا مرنًا أو
غير اعتيادي أو مُلكًا لمستخدم آخر (Unix). ملف الإعدادات نفسه محمي بفحص
الصلاحيات والملكية والروابط الرمزية.

**نافذة TOCTOU المُقلَّصة.** قراءة `~/.ag/config` تستخدم `os.Lstat` ثم تفتح
المسار، فترفض الروابط الرمزية الظاهرة وتقلّص مساحة الهجوم، لكن سباق نظام
ملفات محدود يبقى بين التحقق والفتح. لا يُزعم إغلاق TOCTOU بالكامل. الحلّ
الأقوى (`openat2` بـ `RESOLVE_BENEATH`، أو فتح بـ `O_NOFOLLOW` ثم `Fstat` على
الواصف المفتوح) مؤجّل عمدًا: البيئة المستهدفة هاتف بمستخدم واحد. الحماية
الحالية ترفض الرابط المرن والملف غير الاعتيادي والمُلك لغير المستخدم.

**عدّ الرموز تخمين.** لا مُرمِّز داخل البرنامج؛ التقدير قاعدة تُميّز الأحرف
غير اللاتينية وتميل للمبالغة. تُعاير من `input_tokens` الحقيقية إن وصلت من
المزوّد. توقّع خطأً بحدود ١٠–٢٠٪ حتى تُعايَر.

**قراءة الملفات محدودة الحجم.** `read_file` يقصّ عند `NABD_MAX_READ` بايت
(افتراضي 3072، معايرة لمزوّد بسقف 8000 رمزًا في الدقيقة). القصّ على حدّ
سطر، والنتيجة تخبر النموذج صراحةً بالنطاق المقروء وكيف يتابع بـ `offset`.
القيم خارج [512, 1MiB] أو غير الرقمية تُهمَل ويعود الافتراضي — صفر لا
يعني قراءة فارغة. مزوّدٌ بسقف أعلى يرفع المتغيّر بلا تعديل كود.

**سطرٌ أطول من الحدّ لا يُقرأ كاملًا أبدًا.** إن تجاوز سطرٌ وحده
`NABD_MAX_READ`، يُعرض مقتطعًا ومُعلَّمًا، ويقفز `next_offset` إلى ما بعده —
**بقية ذلك السطر غير قابلة للقراءة بهذه الأداة** (لا يوجد `offset` بايتي)،
وتقول العلامة ذلك صراحةً. القصّ دائمًا على حدود الأحرف (UTF‑8 سليم).

**الملخّص نداء نموذج.** إن فشل، يسقط إلى ملخّص ميكانيكي يذكر طلبات المستخدم
حرفيًا والملفات التي مُسّت. لن تفقد الخيط، لكن ستفقد التفاصيل.

**عمليةٌ واحدة في المجلد الواحد.** نسختان من `ag` في نفس المستودع لن تريا
تعديلات بعضهما أثناء التشغيل، و`/undo` في إحداهما لا تعرف شيئًا عن تعديلات
الأخرى غير المُلحَقة بعد. لكن التراجع نفسه يعيش في سجلّ الجلسة: بعد خروج
العملية، `ag --continue` ثم `/undo` يستعيد آخر تعديل من أحداث السجل، لا من
ذاكرة كانت ستموت.

**عقد التراجع.** `/undo` يتحقق أولًا أن الملف ما زال كما تركته الكتابة
(بمقارنة هاش المحتوى في سجلّ الجلسة)؛ إن غيّره إنسان أو أداة أخرى بعده،
يرفض التراجع ولا يدهس العمل. التراجع لا يغطي أي أثر جانبي لـ `bash` — ما
لم يمرّ عبر أدوات الكتابة لم يُسجَّل ولم يُعَد. التراجع مرّتان على نفس
التعديل: الثانية ترفض، لأن الشرط الأول (الهاش) لم يعد متحققًا.

**`killGroup` يقتل الخلفية المقصودة أيضًا.** `npm run dev &` لن ينجو بعد
انتهاء الأمر. هذا اختيار: خادمٌ يعيش بعد الأمر الذي أنشأه هو إذنٌ بلا انتهاء
صلاحية.

**Linux وmacOS فقط.** `Setpgid` و`sh -c` يمنعان ويندوز.

**`-feed` تستهلك الشاشة البديلة.** الواجهة الافتراضية تطبع في الشاشة
الأساسية، فبعد الخروج يبقى ما رأيته في تمرير الطرفية ويمكن الرجوع إليه
بإصبعك. واجهة `-feed` تعمل بـ `tea.WithAltScreen()`: التمرير أثناء الجلسة
ملك الواجهة (`PgUp`/`PgDn`، `Home`/`End`)، وعند الخروج تعود الطرفية إلى ما
كانت عليه **بلا أثر للجلسة في تمرير الطرفية**. هذا مقصود، لأن إطارات
الارتفاع الكامل في الشاشة الأساسية كانت تُغرق التمرير بنسخ من نفس الشاشة على
Termux. ما تخسره من تمرير الطرفية يعوّضه السجل: يُطبع مسار `session:` عند
الخروج، و`ag --replay` يعرضه حرفًا بحرف. الأخطاء التي تعود من الحلقة تدخل
الخلاصة كسطر دائم قابل للتمرير، لا كحالة عابرة تختفي مع أول ضغطة.

**ما هو مُختبَر وما ليس كذلك.** طبقات المسار والتخزين والأذونات والتراجع
والضغط والصدفة مغطّاة باختبارات وحدة، والتراجع عبر إعادة التشغيل
(`ag --continue` ثم `/undo`) مجرَّب آليًا على جلسة حقيقية. المسار الكامل —
نموذج حيّ يطلب أداة، والنتيجة تعود إليه في الدور التالي — مُجرَّب يدويًا لا
## موجه المزودين المتعدد (Native Multi-Provider Router)

بدءًا من الإصدار v1.2.0، يتيح `nabd` توجيه الطلبات عبر قائمة مرتبة ومغلقة من المزودين والنماذج مع انتقال احتياطي آمن (fallback) يقع **قبل بدء إخراج النموذج فقط**.

### مثال الإعداد (`~/.ag/config`)

```ini

 master
NABD_PROVIDER=router
NABD_ROUTER_MODE=fallback
NABD_ROUTES=groq:model-a,openrouter:model-b:free,nvidia:model-c
GROQ_API_KEY=...
OPENROUTER_API_KEY=...
NVIDIA_API_KEY=...
EOF
```

Config v2 uses `NABD_CONFIG_V2` or `~/.ag/config.v2.json`. It is strict JSON, rejects unknown fields and trailing documents, requires explicit credential sources (`env` or an absolute secure file), rejects command credentials, and cannot be enabled together with v1. Custom `base_url` is deliberately unsupported by the minimal v2 schema.

Do not put credentials in project files. See [docs/THREAT_MODEL.md](docs/THREAT_MODEL.md) for exact guarantees and residual risks. Report suspected escapes through [SECURITY.md](SECURITY.md), not a public issue.

## Headless behavior

No TTY is read. The default `--permission-mode` is `deny`; a tool that would prompt is denied as a tool result rather than blocking. `allow-reads` auto-allows only ReadOnly tools. `ask` still denies without reading a terminal.

stdout contains only the final assistant text, or JSONL with `--json`. Notices and `session:` go to stderr. Sessions are still written under `~/.ag/sessions`, so `--continue` and `--replay` work.

| Exit code | Meaning |
|---|---|
| 0 | settled |
| 1 | provider or tool error |
| 2 | turn ceiling |
| 3 | rate-limit budget exhausted |
| 4 | permission denied with no model answer |
| 130 | interrupted |

## Supported platforms

| GOOS/GOARCH | Status |
|---|---|
| android/arm64 | reference (Termux) |
| linux/amd64 | supported |
| linux/arm64 | supported |
| darwin/arm64 | supported |
| darwin/amd64 | supported |
| windows/* | **not supported** |

Windows is not a release target. Some platform helper files compile there, but the agent's shell execution contract is Unix-oriented.

## Core architecture

- `internal/agent`: event contract, loop, history tree, compaction, budgets.
- `internal/config`: secure v1 and strict v2 configuration loading.
- `internal/store`: append-only JSONL journal.
- `internal/provider`: Anthropic and OpenAI-compatible providers plus ordered fallback router.
- `internal/tools`: path containment and read/write/edit/grep/bash tools.
- `internal/perm`: permission gate.
- `internal/snap`: content-addressed shadow store and undo.
- `internal/ui`: live display and replay.
- `cmd/ag`: wiring; the output binary is `nabd`.

The event is the contract: live rendering and replay consume the same append-only records. `/rewind` appends a new branch point rather than deleting history. `/undo` is intentionally separate and covers tracked file edits, not arbitrary approved shell effects.

## Operational limits

- Token counts are estimates until calibrated from provider usage.
- File reads are bounded; provider-specific limits and `NABD_MAX_READ` determine the cap.
- Compaction may call the model and falls back to a mechanical summary on failure.
- `bash` runs after explicit permission, outside path containment. Treat approval as access equivalent to the current OS user.
- Session journals and the shadow store contain cleartext working data and are sensitive even with private filesystem modes.

## License

MIT License. Copyright (c) 2026 nabd contributors. See [LICENSE](LICENSE).
