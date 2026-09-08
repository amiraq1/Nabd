# nabd — نبض

وكيل برمجة طرفي، يُكتب ويُشغَّل من هاتف. بلا حاويات، بلا قاعدة بيانات، بلا CGO.
سبعة آلاف سطر Go تقريبًا، ملف تنفيذي واحد، وسجلّ جلسة واحد يمكن قراءته بـ `cat`.

**A terminal coding agent built on a phone.** Append-only event journal,
default-deny permissions, git-independent undo. The containment story is
[docs/THREAT_MODEL.md](docs/THREAT_MODEL.md) — not this README.

The installable binary is `nabd`. It was previously built as `ag`, which
collides with the_silver_searcher on a typical PATH. The package path remains
`./cmd/ag`.

الثنائي الذي تُثبّته هو `nabd`. كان يُبنى سابقًا باسم `ag`، وهذا يصطدم بـ
the_silver_searcher على معظم مسارات PATH. مسار الحزمة يبقى `./cmd/ag`.

---

## لماذا

معظم وكلاء البرمجة يخفون ما فعلوه خلف واجهة جميلة. `nabd` يقلب الترتيب:
الجلسة سجلّ أحداث قبل أن تكون واجهة، والواجهة مجرّد قارئ لذلك السجلّ.
كل ما تراه أثناء التشغيل يمكن إعادة عرضه بعده حرفًا بحرف، لأن الحيّ والمُعاد
يمرّان على نفس دالة العرض.

هذا ليس تفصيلًا جماليًا. هو ما يجعل التراجع ممكنًا، والتدقيق ممكنًا،
واستئناف الجلسة أمس ممكنًا اليوم.

## التشغيل

```sh
go build -o nabd ./cmd/ag
# or: ./build.sh

export ANTHROPIC_API_KEY=...      # أو
export NVIDIA_API_KEY=nvapi-...   # أي مزوّد يتكلّم لهجة OpenAI

./nabd                            # محادثة جديدة في المجلد الحالي
./nabd --continue                 # استئناف آخر جلسة
./nabd --replay <file.jsonl>      # إعادة عرض جلسة، --speed 0 للفوري
./nabd -feed                      # واجهة الشاشة الكاملة (تجريبية)
./nabd --version                  # version · commit · date

./nabd -p "task text"             # headless: answer on stdout, exit
./nabd -p - < file                # task from stdin
./nabd -p "..." --json            # journal JSONL on stdout
./nabd -p "..." --max-turns 8
./nabd -p "count the go files" --permission-mode allow-reads
```

### Headless (`-p`)

No TTY. The Asker never blocks. Default `--permission-mode` is `deny`:
`agent.Decision(0)==Deny` is literal. A tool that would prompt is denied as a
`tool_result` (and a journal Event); the run is not killed. `allow-reads` still
auto-allows ReadOnly tools. `ask` still never reads a tty — it denies without
blocking. YOLO is not used.

stdout is only the final assistant text, or JSONL with `--json` (same schema as
`session.jsonl`). Notices and `session:` go to stderr. ANSI is not emitted.
The session is still written under `~/.ag/sessions`, so `--continue` and
`--replay` work.

Exit codes:

| code | meaning |
|---|---|
| 0 | settled |
| 1 | provider or tool error |
| 2 | turn ceiling (`ErrMaxTurns`) |
| 3 | rate-limit budget exhausted (`ErrRateLimitBudget`) |
| 4 | permission denied and the model produced no answer |
| 130 | interrupted |

Release binaries (static, `CGO_ENABLED=0`, trimpath, ldflags-stamped) are
published on tag `v*`. See `docs/RELEASING.md`. `v1.2.0` already exists;
the Goreleaser pipeline is for **v1.3.0** onward.

Put keys in `~/.ag/config` with mode `600`, not in the environment:

```sh
mkdir -p ~/.ag && touch ~/.ag/config && chmod 600 ~/.ag/config
cat >> ~/.ag/config <<'EOF'
ANTHROPIC_API_KEY=sk-ant-...
NABD_MODEL=claude-sonnet-4-5
EOF
```

Format is `KEY=VALUE` per line (`export`, quotes, `#` comments tolerated).
`NABD_CONFIG` changes the path. What the file guarantees, what it does not,
and what to do instead: [docs/THREAT_MODEL.md](docs/THREAT_MODEL.md).
Report an escape with [SECURITY.md](SECURITY.md), not a public issue.

Optional knobs (file or environment): `NABD_PROVIDER`, `NABD_MODEL`,
`NABD_BASE_URL`, `NABD_CTX`, `NABD_MAX_TOKENS` (default 1024, range 128–8192).

In-session: `/undo [n]`, `/edits`, `/rewind [n]`, `/ctx`, `/compact`, `/help`.
At a permission prompt: `y` once, `a` for the rest of the session (Mutating
only), `n` deny. `ctrl+c` stops the turn, `ctrl+d` exits.

## Supported platforms

| GOOS/GOARCH | Status |
|---|---|
| android/arm64 | reference (Termux) |
| linux/amd64 | supported |
| linux/arm64 | supported |
| darwin/arm64 | supported |
| darwin/amd64 | supported |
| windows/* | **not supported** |

Windows is not a release target: the bash tool uses `syscall.Kill` and
`Setpgid`. Snap has Windows rename/sync helpers, but the agent does not
build or run as a supported platform.

ويندوز غير مدعوم كمنصة تشغيل.

## المعمار في ستّ فقرات

**الحدث هو العقد.** كل ما يجري — رسالة، جزء نصّ، نداء أداة، سؤال إذن، خطأ،
مقاطعة — حدثٌ بحقول مسطّحة يُلحق بملف `session.jsonl` ولا يُعدَّل بعدها.
الواجهة تتغيّر كل يوم؛ هذا الملف لا.

**الملف شجرة لا قائمة.** كل حدث يحمل `parent`، فالسجلّ المُلحَق فقط يصف شجرة.
`/rewind` لا يحذف شيئًا: يُلحق حدثًا يشير أبوه إلى ما قبل الدور المقصوص.
القصّ إضافة، لا حذف.

**القيم الصفرية والحصر والتراجع** ليست فقرات README. الضمانات، وما هو
مخفَّض، وما هو خارج النطاق، مع أسماء الاختبارات:
[docs/THREAT_MODEL.md](docs/THREAT_MODEL.md).

**الضغط قيدٌ لا مقصّ.** حين يقارب السياق ثلاثة أرباع النافذة تُلحق إدخالة
`compact` تحمل `first_kept` وملخّصًا؛ لا يُعاد كتابة شيء. الحدّ يقع على رسالة
مستخدم حصرًا، لأن القطع داخل دور يترك `tool_result` بلا `tool_use` فيُرفض
الطلب التالي كليًا. وقبل الملخّص الغالي يمرّ مقصّ رخيص يُفرغ مخرجات الأدوات
القديمة ويُبقي الأخطاء — الخطأ قصير ويمنع التكرار.

## الحدود التشغيلية

**عدّ الرموز تخمين.** لا مُرمِّز داخل البرنامج؛ التقدير قاعدة تُميّز الأحرف
غير اللاتينية وتميل للمبالغة. تُعاير من `input_tokens` الحقيقية إن وصلت من
المزوّد. توقّع خطأً بحدود ١٠–٢٠٪ حتى تُعايَر.

**قراءة الملفات محدودة الحجم.** `read_file` يقصّ عند `NABD_MAX_READ` بايت
(افتراضي 3072). القصّ على حدّ سطر، والنتيجة تخبر النموذج بالنطاق وكيف يتابع
بـ `offset`. القيم خارج [512, 1MiB] تُهمَل. إن تجاوز سطرٌ وحده السقف، يُعرض
مقتطعًا ويقفز `next_offset` إلى ما بعده — بقية ذلك السطر غير قابلة للقراءة
بهذه الأداة.

**الملخّص نداء نموذج.** إن فشل، يسقط إلى ملخّص ميكانيكي يذكر طلبات المستخدم
حرفيًا والملفات التي مُسّت.

**`-feed` تستهلك الشاشة البديلة.** الواجهة الافتراضية تطبع في الشاشة
الأساسية. `-feed` يستخدم `tea.WithAltScreen()`: عند الخروج لا يبقى أثر في
تمرير الطرفية. التعويض هو السجل: يُطبع `session:` عند الخروج، و
`nabd --replay` يعيد العرض.

احتواء المسارات، `bash`، المفاتيح، والتراجع: ليس هنا.
[docs/THREAT_MODEL.md](docs/THREAT_MODEL.md).

## موجه المزودين المتعدد (Native Multi-Provider Router)

بدءًا من الإصدار v1.2.0، يتيح `nabd` توجيه الطلبات عبر قائمة مرتبة ومغلقة من المزودين والنماذج مع انتقال احتياطي آمن (fallback) يقع **قبل بدء إخراج النموذج فقط**.

### مثال الإعداد (`~/.ag/config`)

```ini
NABD_PROVIDER=router
NABD_ROUTER_MODE=fallback
NABD_ROUTER_PRESTREAM_TIMEOUT=30
NABD_ROUTES=groq:model-a,openrouter:model-b:free,nvidia:model-c
```

### القواعد والعقود

1. **قواعد الصياغة (Grammar):**
   - يُفصل الزوج عند **أول نقطتين `:` فقط**؛ ما قبلها هو اسم المزود (`provider`)، وما بعدها هو اسم النموذج (`model`).
   - يُسمح لاسم النموذج باحتواء نقطتين `:` (مثل `openrouter:model-b:free`).
   - المزودون المسموح بهم فقط: `anthropic`, `groq`, `openrouter`, `nvidia`.
   - الفاصلة `,` هي الفاصل الوحيد بين المسارات (بين 1 إلى 16 مسارًا كحد أقصى). لا يمكن الهروب منها.
   - يُرفض التكرار للزوج المتطابق تمامًا (`provider:model`). يُسمح باستخدام نفس المزود مع نماذج مختلفة (مثل `groq:model-a,groq:model-b`).

2. **عقود المتغيرات والبيئة:**
   - `NABD_ROUTER_MODE`: القيمة المدعومة الوحيدة هي `fallback`.
   - `NABD_ROUTER_PRESTREAM_TIMEOUT`: مهلة بدء الاستجابة بالثواني لكل مسار على حدة (بين 5 و 120 ثانية، افتراضيًا 30s).
   - `NABD_MODEL`: يُهمل في نمط الراوتر.
   - `NABD_BASE_URL`: يُرفض مع الراوتر.
   - كل مزود يحتاج مفتاحه في `~/.ag/config` أو البيئة. كيف تُحمى المفاتيح:
     [docs/THREAT_MODEL.md](docs/THREAT_MODEL.md).

3. **الالتزام والانتقال الاحتياطي:** fallback قبل أول قطعة دلالية فقط.
   بعد الالتزام لا انتقال. تنظيف المسار الفاشل بمهلة 2s.

4. **قيود v1.2.0:** لا قاطع دائرة، إلغاء بعيد غير مضمون، سباق فوترة مزدوجة
   ممكن، أسوأ زمن انتظار = عدد المسارات × (مهلة ما قبل الإخراج + التنظيف).

## البنية

`internal/agent` عقد الحدث والحلقة والشجرة والضغط والميزانية.
`internal/config` قراءة `~/.ag/config`؛ لا يكتب شيئًا أبدًا.
`internal/store` سجلّ JSONL بإلحاق ذرّي.
`internal/provider` واجهة المزوّد وتنفيذان: Anthropic، وأي خادم بلهجة OpenAI.
`internal/tools` الاحتواء وأدوات القراءة والكتابة والصدفة.
`internal/perm` بوّابة الأذونات.
`internal/snap` الظلّ والتراجع.
`internal/ui` العرض والمحادثة وإعادة العرض.
`internal/build` طوابع الإصدار لـ `--version` وراية RunStart.
`cmd/ag` الربط. الثنائي الناتج اسمه `nabd`.

قاعدة اتجاه واحدة: `agent` لا يستورد `tools` أبدًا.

## الأصل

كُتب من الصفر. سبقته قراءةٌ في معمار وكلاء آخرين لفهم القرارات لا لنسخها.
غير مرتبط بأي مزوّد نماذج ولا مدعوم منه.

## الرخصة

MIT License. Copyright (c) 2026 nabd contributors. Full text in `LICENSE`.
