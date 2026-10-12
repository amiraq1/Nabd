# تقرير تحقق: تصنيف الاختبارات السلبية للمهام 1/3/4

**الحالة: متوقف عند شرطي توقف. لم يُشغَّل أي اختبار. لا بند مُغلق.**

---

## 1. الأساس (الخطوة 0)

| البند | القيمة |
|---|---|
| `git rev-parse HEAD` | `9f89de824e8ed3cd9186f8a37781d570c6ca69d2` |
| `git branch --show-current` | `ci/threat-model-gate-covers-display` |
| `go version` | `go1.27.1 windows/amd64` |
| `go env GOOS GOARCH` | `windows` / `amd64` |
| نظام التشغيل | `MINGW64_NT-10.0-26200` (MSYS on Windows 11) |

### تغييرات غير متتبعة/معدّلة تحت `internal/tools` (مُسجّلة قبل المتابعة)

```
 M internal/tools/excluded_read_test.go
 M internal/tools/registry.go
 M internal/tools/undo.go
 M internal/tools/write_commit.go
?? internal/tools/p0_2_test.go
?? internal/tools/p0_3_test.go
?? internal/tools/p0_4_test.go
?? internal/tools/p0_enforcement_test.go
?? internal/tools/p0_undo_red_test.go
?? internal/tools/protected.go
```

**ملاحظة حاسمة:** الاختبارات الخمسة المطلوب تصنيفها **ليست موجودة في الـSHA** — هي ملفات غير متتبعة في شجرة العمل فقط. أي أن «تشغيلها على الـSHA» يعني تشغيل ملفات غير متتبعة مقابل كود الـSHA، وهو ما يتطلب إعداد شجرة منفصلة (`git archive 9f89de8` + نسخ الاختبارات + scaffolding).

### الـSHA الوحيد المُختبر

`9f89de8` فقط. أي فرع أو commit آخر خارج النطاق.

---

## 2. نتيجة الخطوة 2 — أي نسخة من المسار المستثنى موجودة؟

فرضية المهمة كانت أن الملف يحتوي **نسختين متعارضتين**. النتيجة الفعلية تنفي ذلك في **الـSHA وفي شجرة العمل معاً**.

### عند الـSHA `9f89de8`

| العنصر | الحالة | الموضع |
|---|---|---|
| `func commitExcluded` | **غير موجود إطلاقاً** | — |
| `func buildExcludedRecord` | موجود | `internal/tools/write_commit.go:309` |
| `func commit` | موجود | `internal/tools/write_commit.go:149` |
| فرع المسار المستثنى | `if reg != nil && reg.isPathExcluded(relative)` | `write_commit.go:190` |
| استدعاء الباني | `rec, rerr = buildExcludedRecord(sh, before, after, data, readLines)` | `write_commit.go:191` |
| `OnMutationPrepared` | **يُستدعى** | `write_commit.go:207-208` |
| `os.Lstat` / `os.ReadFile` في الملف | **لا وجود لهما** | — |

**الاستنتاج:** عند الـSHA توجد **النسخة الأولى فقط** (التي تستدعي `buildExcludedRecord` وتستدعي `OnMutationPrepared`). النسخة الثانية الموصوفة في المهمة (`commitExcluded` بقراءة `os.Lstat`/`os.ReadFile` وبلا `OnMutationPrepared`) **لا وجود لها**.

و`buildExcludedRecord` عند الـSHA لا يقرأ الملف مسبقاً ولا يطبّق حدّاً — يستقبل `before, after snap.State` جاهزين:

```go
func buildExcludedRecord(sh *snap.Shadow, before, after snap.State, data []byte, readLines int) (*event.EditRecord, error) {
	rec := &event.EditRecord{
		MutationID: mutationID(),
		Path:       after.Rel,
		HashAfter:  sha256hex(data),
		ReadLines:  readLines,
		BlobAfter:  after.Blob,
		BlobBefore: before.Blob,
		ModeBefore: before.Mode,
	}
	...
```

### في شجرة العمل (غير مُلتزَمة)

| العنصر | الحالة | الموضع |
|---|---|---|
| `func commitExcluded` | موجود (نسخة **واحدة**) | `write_commit.go:416`، مُستدعاة من `:252` |
| `func buildExcludedRecord` | **أُزيل بالكامل** (يبقى ذكره في تعليقات اختبار فقط) | — |
| `OnMutationPrepared` | يُستدعى | `write_commit.go:453-454` |
| `OnMutationAborted` | يُستدعى | `write_commit.go:460-461` |
| `os.ReadFile` / `os.Lstat` في الملف | **تعليق فقط** عند `:410` ينص على عدم استخدامهما | — |

**الاستنتاج:** لا توجد نسختان. توجد نسخة واحدة، ولا تستخدم `os.ReadFile`/`os.Lstat` على المسار المستثنى، ولا تُسقط `OnMutationPrepared`.

> ملاحظة: `os.Lstat`/`os.ReadFile` في `internal/tools` توجد في `grep.go:157`، `open_regular_other.go:16`، `read_source_other.go:21` — وكلها مسارات توافق `!unix` أو منطق grep، **لا علاقة لها بالمسار المستثنى**.

**⇒ شرط التوقف الثالث مُفعَّل: «تبيّن أن الـSHA لا يحتوي النسخة التي تتوقعها من `commit`».**

---

## 3. الخطوة 1 — الترجمة

```
$ GOOS=linux GOARCH=amd64 go vet ./internal/tools/
VET_EXIT=0
(لا مخرجات)

$ GOOS=linux GOARCH=amd64 go test -c -o /dev/null ./internal/tools/
EXIT=0
(لا مخرجات)
```

**رموز السكافولد — الحالة الفعلية:** معرّفة في شجرة العمل (لأن الإصلاح مُطبَّق فيها)، وغير معرّفة عند الـSHA:

| الرمز | الموضع في شجرة العمل | عند الـSHA |
|---|---|---|
| `excludedGuard` | `write_commit.go:178` | غير موجود |
| `ErrExcludedWrite` | `write_commit.go:183` | غير موجود |
| `maxExcludedReadBytes` | `write_commit.go:190` | غير موجود |
| `ErrUndoExcluded` | `undo.go:18` | غير موجود |
| `UndoResult.Err` | `undo.go:25` | غير موجود |
| `approvedCtx` | `p0_4_test.go:50` (دالة اختبار) | غير موجود |

**توقيع `approvedCtx` كما هو في الشفرة** (`p0_4_test.go:50`):

```go
func approvedCtx(path string) context.Context {
	return approval.WithToken(approval.WithCall(context.Background(), "test-id"), approval.Mint(path, "test-id"))
}
```

**مطابقة التوقيع للاستدعاء:** ✅ جميع مواضع الاستدعاء العشرة تمرّر سلسلة واحدة كأول وسيط — `commit(approvedCtx("big.bin"), ...)` و`r.Run(approvedCtx("dist/bundle.js"), ...)`. مطابق.

---

## 4. الخطوة 3 — التشغيل: **لم يُنفَّذ** (شرط التوقف الأول)

### Linux غير متاح — الأدلة

```
docker: absent    podman: absent    limactl: absent
multipass: absent colima: absent    nerdctl: absent
wsl: present (مُدرَج في القائمة السوداء الأمنية — الحجب يمنع إعادة المحاولة أو الالتفاف)
```

والدليل القاطع أن مخرجات الترجمة المتقاطعة لا يمكن تنفيذها هنا:

```
$ GOOS=linux go test -run TestP01 -count=1 ./internal/tools/
fork/exec C:\Users\Aledari\AppData\Local\Temp\go-build...\b001\tools.test: %1 is not a valid Win32 application.
FAIL	nabd/internal/tools	0.059s
```

**ورفض مسار Windows ليس تفضيلاً بل مطلب المهمة:** `internal/safefs/open_unix.go` (وسم `//go:build unix`) هو المسار الذي يرفض الروابط؛ وعلى `!unix` يُرجع `OpenRead` الخطأ `ErrUnsupportedPlatform` (`open_other.go:14`). أي أن سلوك `safefs` على Windows **مختلف جوهرياً**، فتشغيل المجموعة هناك لا يقيس نفس الشفرة.

**لذلك:** لا سجلات RED/GREEN، ولا استيفاء للقاعدة 1، ولا تصنيف RED مثبت.

---

## 5. الخطوة 4 — رفض الروابط الرمزية في `safefs`: **مُثبت في الشفرة**

| السطر | الدليل |
|---|---|
| `internal/safefs/open_unix.go:24` | `const openFlagsDir = unix.O_RDONLY \| unix.O_CLOEXEC \| unix.O_NOFOLLOW \| unix.O_NONBLOCK \| unix.O_DIRECTORY` |
| `internal/safefs/open_unix.go:30` | `const openFlagsFile = unix.O_RDONLY \| unix.O_CLOEXEC \| unix.O_NOFOLLOW \| unix.O_NONBLOCK` |
| `internal/safefs/open_unix.go:79` | `fd, err := unix.Openat(prev, comp, flags, 0)` — كل مُكوِّن يُفتح بـ`O_NOFOLLOW` نسبةً إلى واصف المُكوِّن السابق |
| `internal/safefs/open_unix.go:121-123` | `if errors.Is(err, unix.ELOOP) { return fmt.Errorf("%w: %s", ErrSymlink, path) }` — `ELOOP` هو ما ينتجه `O_NOFOLLOW` عند مكوِّن أخير رابط |
| `internal/safefs/open_unix.go:133-135` | `if errors.Is(err, unix.ELOOP) \|\| errors.Is(err, unix.ENOTDIR) { return fmt.Errorf("%w: %s", ErrNotDirectory, path) }` |
| `internal/safefs/open_unix.go:57` | `root, err := filepath.EvalSymlinks(rootPath)` — يُحلّ **الجذر وحده** مرة واحدة (موثّق: الجذر موثوق ومثبَّت عند الإقلاع) |

**الخلاصة الساكنة:** رفض الروابط **موجود ومُثبت في الشفرة**، فلا ينطبق عليه «غير متحقق» بمعنى غياب الدليل. لكن هذا **دليل ساكن وليس نتيجة تشغيل**، فلا يجيز تصنيف «حارس» كحقيقة مُختبَرة.

---

## 6. الخطوة 5 — جدول التصنيف النهائي

| الاختبار | النتيجة الفعلية | رسالة الفشل (حرفياً) | السبب المكتوب في التعليق | التطابق | التصنيف النهائي |
|---|---|---|---|---|---|
| `TestP01CommitRefusesNilRegistry` | لم يُشغَّل | — | يحرس HEAD بـ`reg != nil &&` فيمرّ nil إلى الكتابة | غير متحقق | **غير متحقق** |
| `TestP03ExcludedFileOverLimitRefused` | لم يُشغَّل | — | HEAD لا يقرأ الملف ولا يطبّق حدّاً | غير متحقق | **غير متحقق** |
| `TestP03ExcludedFileChangedBetweenReadAndPublishRefused` | لم يُشغَّل | — | `verifyUnchanged` موجود مسبقاً ⇒ حارس | غير متحقق | **غير متحقق** |
| `TestP04ExcludedWriteJournalsIntent` | لم يُشغَّل | — | HEAD يسجّل blob ids حقيقية بدل الحارس | غير متحقق | **غير متحقق** |
| `TestP04ExcludedWriteRefusedWhenIntentJournalingFails` | لم يُشغَّل | — | `OnMutationPrepared` مُستدعى مسبقاً ⇒ حارس | غير متحقق | **غير متحقق** |
| `TestP01SymlinkToExcludedTargetIsRefused` | لم يُشغَّل | — | `safefs` يرفض الروابط أصلاً ⇒ حارس | غير متحقق | **غير متحقق** |

**لا يوجد أي `Skip` مسجَّل**، لأن أي اختبار لم يُنفَّذ أصلاً. القاعدة 7 (الاختبار المتخطى لا يُحسب) لا تنطبق هنا، لكن لا شيء يُحسب إيجاباً.

### ملاحظات ساكنة (قراءة شفرة — **ليست** تصنيفاً ولا تُغني عن التشغيل)

هذه مُدرجة للسياق فقط، والقاعدة 1 تمنع ترقيتها إلى «RED مثبت» دون سجل:

- `TestP01CommitRefusesNilRegistry`: عند الـSHA، `commit()` يقيّد فرع الاستثناء بـ`reg != nil &&` (`:190`)، فقيمة `nil` تتخطى قرار الاستثناء وتصل إلى `buildRecord` ثم `writeFromRoot`. **السلوك المُتوقَّع ساكناً: كتابة ناجحة** ⇒ الاختبار المتوقّع أن يفشل عند أول تحقق.
- `TestP03ExcludedFileOverLimitRefused`: عند الـSHA لا وجود لـ`maxExcludedReadBytes` ولا قراءة للملف في `buildExcludedRecord` (`:309`) ⇒ **السلوك المُتوقَّع ساكناً: كتابة ناجحة**.
- `TestP04ExcludedWriteJournalsIntent`: عند الـSHA `buildExcludedRecord` يملأ `BlobBefore`/`BlobAfter` بـ`before.Blob`/`after.Blob` الحقيقيين (`:317-318`) لا بالحارس ⇒ التحقق الأول (استدعاء واحد) ينجح، وتحقق الحارس يفشل.
- `TestP03ExcludedFileChangedBetweenReadAndPublishRefused` و`TestP04ExcludedWriteRefusedWhenIntentJournalingFails`: عند الـSHA كلٌّ من `verifyUnchanged` و`OnMutationPrepared` قائم (`:207` وما بعده) ⇒ **متوقّع أن ينجحا قبل الإصلاح**، فهما حارسان لا اختباران سلبيان.

---

## 7. الخطوة 6 — تعديلات التعليقات: **لا شيء هذه الجولة**

لم أُعدّل أي تعليق ولا أي منطق اختبار في هذه الجولة: القاعدة 6 تشترط أن يطابق التعليق **سجل التشغيل**، ولا سجل تشغيل موجود (لم يُشغَّل أي اختبار)، وشرطا التوقف يُلزمانني بالتوقف عن المتابعة.

**إفصاح إلزامي:** في جولة **سابقة** (وليس هذه) عُدِّلت رؤوس وتعليقات `p0_enforcement_test.go` و`p0_undo_red_test.go` لتمييز «حارس» عن «RED» ولفصل «الترجمة مُتحقَّقة» عن «التشغيل غير مُتحقَّق». تلك التعديلات كانت مبنية على **قراءة ساكنة لكود الـSHA**، لا على سجل تشغيل. لم تُعدَّل منطق أي اختبار. الأسطر المتأثرة في `p0_enforcement_test.go` هي رؤوس التعليقات قبل الأسطر 35، 71، 78، 108، 136، 160؛ وفي `p0_undo_red_test.go` قبل الأسطر 22، 55، 68، 90، 102. تبقى جميعها **غير متحققة** حتى تُسنَد بسجل تشغيل.

**تناقض موثَّق لم أُصلحه (خارج نطاق هذه الجولة):** تعليق `TestP03ExcludedFileOverLimitRefused` يقول «`maxExcludedReadBytes` غير مستخدم في الكود القديم، فخفضه هناك خامل». الأدق: الرمز **غير معرّف** عند الـSHA (السكافولد يوفّره) ولا شيء يستشيره. الفرق دلالي؛ تركتُه لأن التصحيح يلزمه سجل تشغيل وليس قراءة ساكنة.

---

## 8. المخاطر والمتبقي

1. **لا تصنيف RED مثبت لأي اختبار.** القاعدة 1 غير مستوفاة، ولا يجوز إغلاق أي بند.
2. **فرضية المهمة حول «نسختين متعارضتين» غير صحيحة** لـ`9f89de8` ولشجرة العمل. يُرجَّح أنها مأخوذة من لقطة وسيطة أثناء جولة تحرير سابقة؛ تلك الحالة لم تعد قائمة. هذا وحده يوجب إعادة صياغة المهمة قبل تكرارها.
3. **الاختبارات المستهدفة ليست في الـSHA** — هي ملفات غير متتبعة. أي جولة تحقق حقيقية تحتاج أولاً التزام الـscaffolding والاختبارات، أو بناء شجرة منفصلة.
4. **لا مشغّل Linux** في هذه البيئة. تشغيل الجولة يتطلب CI أو جهاز Linux/WSL مُصرَّح به.
5. **لم يُتحقق من** `TestP05*` (خارج نطاق هذه المهمة، وهي في `p0_undo_red_test.go`) ولا من المهمة 2 ولا المهمة 7.
6. **الرابط الرمزي:** الدليل ساكن (`O_NOFOLLOW`) ويحتاج تشغيلاً لتثبيت سلوك الحارس؛ ويجب تسجيل `SKIP` صراحةً إن ظهر في بيئة بلا دعم روابط.

---

## 9. الإعلان النهائي

**لا يوجد بند مغلق.** لم يُشغَّل أي اختبار، ولم يُصنَّف أي اختبار كـRED مثبت، ولم تُعدَّل أي شفرة إنتاج أو منطق اختبار في هذه الجولة.
