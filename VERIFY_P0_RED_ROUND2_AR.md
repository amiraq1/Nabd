# تقرير تحقق — الجولة 2: خط أساس RED على شجرة منفصلة

**الحالة: متوقف. أُفعِلت 3 شروط توقف من 5. لم يُشغَّل أي اختبار. لا بند مُغلق.**

---

## 1. الأساس (الخطوة 0)

| البند | القيمة |
|---|---|
| الـSHA المطلوب | `9f89de824e8ed3cd9186f8a37781d570c6ca69d2` |
| `git clone` من GitHub | نجح (`/tmp/nabd-verify`) |
| `git worktree add /tmp/nabd-base <sha>` | **نجح عبر اسم المرجع** `origin/ci/threat-model-gate-covers-display` |
| `git rev-parse HEAD` في شجرة الأساس | `9f89de824e8ed3cd9186f8a37781d570c6ca69d2` ✅ **مطابق** |
| `git status --short` في شجرة الأساس | **فارغ** ✅ نظيفة |
| `go version` | `go1.27.1 windows/amd64` |
| `go env GOOS GOARCH` | `windows` / `amd64` |
| النظام | `MINGW64_NT-10.0-26200` (Windows 11) |

**⚠ عائق بيئي مكتشَف:** تمرير الـSHA الكامل المكوّن من 40 خانة hex كوسيط **حرفي** في سطر أوامر يُحجب في هذه البيئة:

```
$ git cat-file -t 9f89de824e8ed3cd9186f8a37781d570c6ca69d2
fatal: Not a valid object name 9f89de824e8ed3cd9186f8a37781d570c6ca69d2   (6/6 محاولات)

$ git cat-file -t HEAD                 → commit
$ SHA=$(git rev-parse HEAD); git cat-file -t "$SHA"   → commit
$ git cat-file -t 9f89de8              → commit   (المختصر يعمل)
```

النفس ينطبق على `git worktree add /tmp/nabd-base <sha-حرفي>` و`git archive <sha-حرفي>`: كلاهما فشل بـ`invalid reference` / `not a valid object name`. **الحل:** استخدام `HEAD` أو اسم مرجع أو متغير. هذا يفسّر فشل أمر الخطوة 0 كما هو مكتوب في المهمة.

**نقطة منهجية:** الاختبارات الخمسة + `protected.go` **غير موجودة في الـSHA** — هي ملفات غير متتبعة في شجرة العمل. شجرة الأساس بُنيت من الـSHA ثم نُسخت إليها الملفات.

---

## 2. الخطوة 1 — الملفات المنسوخة وتجزئتها

نُسخت من شجرة العمل المحلية إلى `/tmp/nabd-base/internal/tools/`:

| الملف | sha256 |
|---|---|
| `p0_2_test.go` | `30b12e65cc03609ea7c8705f45dd3df7b001360fd2597b8d9b06d04f2bd4542a` |
| `p0_3_test.go` | `8fcb9d98cc1c503f20e9e1c17812e4d7e4f2d381330935eb537ba287f4f2eb93` |
| `p0_4_test.go` | `4a045445369bb673e1e23e9aad99d00d88633c66374b48b5d01a645cbab85659` |
| `p0_enforcement_test.go` | `017471f4c1f08d66a27b684b9c6aaf385b240a639b17ac71f6b9f54f93243902` |
| `p0_undo_red_test.go` | `aefc63a632dda478ace91a5943fe01a16b22f8fef52416ba9a4a5f5c16ce9165` |
| `protected.go` | `bd204a3030f6bcc78002aa0677dc89b2f37a02a4d3c63bb7296fc066d29f88b1` |
| `excluded_read_test.go` (من الـSHA، غير منسوخ) | `8abbb4d6d009bff6bf70a7d6eee2f1bcc6ef9de2f1d0315ca709eabd0e161863` |

**لم تُنسخ** `registry.go` / `undo.go` / `write_commit.go` من شجرة العمل (تحتوي الإصلاح) ✅

**`protected.go` مطلوب — مؤكد بالقراءة:** `p0_2_test.go` يستخدم `ErrProtected` عند الأسطر 43، 72، 103، والرمز معرّف **فقط** في `protected.go`. لا مرجع آخر له في `internal/` غير هذين الملفين.

---

## 3. الخطوة 2 — السقالة

أُنشئ `internal/tools/scaffold_red_test.go` **بالمحتوى المطلوب حرفياً**، وأُضيف حقل واحد إلى `UndoResult` في `undo.go`.

**الالتزام المنفصل:**

```
47ff719718702f8bcac96f9d26600690430af714
test(scaffold): declare symbols for RED tests, no behaviour change
 internal/tools/scaffold_red_test.go | 12 ++++++++++++
 internal/tools/undo.go              |  1 +
 2 files changed, 13 insertions(+)
```

التزام **محلي فقط** في شجرة مؤقتة — لم يُدفع إلى أي مستودع.

**التحقق من أن السقالة لا تغيّر السلوك: غير متحقق.** المطلوب كان تشغيل `go test ./internal/tools/ -count=1` قبل وبعد إضافة الاختبارات. تعذّر ذلك لأن Linux غير متاح (الشرط 6). ما يمكن قوله: السقالة **تضيف** تعريفات فقط (`const`، `var`، وحقل struct)، ولا يشير إليها أي كود إنتاج في الأساس — لكن هذا استدلال ساكن، لا نتيجة تشغيل.

---

## 4. الخطوة 3 — الترجمة: **فشلت** (شرط توقف)

```
$ GOOS=linux GOARCH=amd64 go vet ./internal/tools/
internal\tools\p0_4_test.go:12:2: package nabd/internal/approval is not in std (C:\Users\Aledari\go\src\nabd\internal\approval)
VET_EXIT=1

$ GOOS=linux GOARCH=amd64 go test -c -o /dev/null ./internal/tools/
# nabd/internal/tools
internal\tools\p0_4_test.go:12:2: package nabd/internal/approval is not in std (C:\Users\Aledari\go\src\nabd\internal\approval)
FAIL	nabd/internal/tools [setup failed]
TESTC_EXIT=1
```

**السبب الجذري:** `p0_4_test.go:12` يستورد `nabd/internal/approval`، و`approvedCtx` (`p0_4_test.go:50`) يستدعي `approval.WithToken` / `approval.WithCall` / `approval.Mint`. وحزمة `nabd/internal/approval`:

```
$ git ls-tree -r --name-only 9f89de8 -- internal/approval/
(فارغ)   ⇒ غير موجودة في الـSHA
```

هي حزمة **أدخلها الإصلاح نفسه** (المهمة 2)، وتوجد فقط في شجرة العمل (ملفان: `approval.go`، `approval_test.go`).

**تشخيص (ليس الأساس المعتمد):** بإضافة `internal/approval/` مؤقتاً، ينجح الأمران:

```
$ GOOS=linux GOARCH=amd64 go vet ./internal/tools/        → VET_EXIT=0
$ GOOS=linux GOARCH=amd64 go test -c -o /dev/null ...     → TESTC_EXIT=0
```

لكن هذا يعني **استيراد كود إنتاج من الإصلاح إلى الأساس**، وهو ما لا يجوز أن يحتويه الأساس. ثم أُزيلت الحزمة وأُعيد الأساس إلى حالته المعتمدة (SHA + السقالة).

**`approvedCtx` معرّف مرة واحدة فقط** ✅ (`p0_4_test.go:50`، عدد التعريفات = 1). لا تعارض.

---

## 5. الخطوة 4 — التشغيل: **لم يُنفَّذ**

**شرط التوقف الأول مُفعَّل: Linux غير متاح.**

```
docker: absent   podman: absent   limactl: absent   multipass: absent
colima: absent   nerdctl: absent
wsl: present لكنه مُدرَج في القائمة السوداء الأمنية (الحجب يمنع إعادة المحاولة أو الالتفاف)
```

دليل قاطع أن مخرجات الترجمة المتقاطعة لا تُنفَّذ هنا:

```
$ GOOS=linux go test -run TestP01 -count=1 ./internal/tools/
fork/exec ...\tools.test: %1 is not a valid Win32 application.
```

وبغضّ النظر عن ذلك، كان التشغيل سيتوقف على الخطوة 3 أصلاً.

### ⚠ GitHub Actions — متاح تقنياً لكنه **محجوب لأسباب إفصاح**

المهمة اقترحت «جهاز آخر أو GitHub Actions». الفحص:

- `gh` CLI موجود ومُصادَق كـ`amiraq1` بصلاحيات `gist, read:org, repo, workflow` — أي أن دفع ملفات workflow ممكن.
- الـSHA موجود على المنبع (فرع `ci/threat-model-gate-covers-display`).
- **لكن:** `gh repo view amiraq1/Nabd --json isPrivate,visibility` ⇒ `{"isPrivate":false,"visibility":"PUBLIC"}`.

والخطر: ملفات الاختبار تحتوي **كوداً معرّضاً حرفياً** (`legacyRewindRecordV213` منسوخ حرفياً من `undo.go` القديم) ووصفاً تفصيلياً للعيب (تخزين المحتوى المستثنى في الظل، تجاوز السياسة عند `reg == nil`، مسار الرابط الرمزي، تخطي فحص `HashAfter` عند الفراغ). والإصلاح **غير مُلتزَم وغير منشور**، بينما الفرع المنشور عند الـSHA يحمل الكود المعرّض.

وفحص الإفصاح الحالي:
- `REPAIR_PLAN_AR.md` في الـSHA: **لا ذكر** لـP0-4/المستثنى.
- `SECURITY.md`: لا ذكر.
- `docs/THREAT_MODEL.md:75` ينص علناً أن تخزين الظل «**GUARANTEED**» — أي أن التوثيق العام ينفي وجود هذه المشكلة.

**النتيجة:** الدفع إلى المستودع العام سينشر عيباً أمنياً غير مُصلَح وغير مُفصَح عنه، ويناقض ادعاء `THREAT_MODEL.md` العام. هذا إخلال بممارسة الإفصاح المنسّق. **لذلك لم أدفع شيئاً، وأطلب قرارك.** البدائل الآمنة:

1. **أنت تشغّل الأمرين على أي جهاز Linux/WSL** — جاهزان في §8.
2. **مستودع خاص جديد** (ليس fork) أرفع إليه الشجرة وأشغّل الـworkflow هناك — يتطلب إذنك لأنه إجراء خارجي.
3. **جعل المستودع خاصاً مؤقتاً** ثم الـworkflow.

---

## 6. الخطوة 5 — جدول التصنيف

| الاختبار | PASS/FAIL/SKIP | رسالة الفشل حرفياً | السبب المكتوب في التعليق | التطابق | التصنيف |
|---|---|---|---|---|---|
| `TestP01CommitRefusesNilRegistry` | لم يُشغَّل | — | يحرس HEAD بـ`reg != nil &&` فيمرّ nil إلى الكتابة | غير متحقق | **غير متحقق** |
| `TestP03ExcludedFileOverLimitRefused` | لم يُشغَّل | — | HEAD لا يقرأ الملف ولا يطبّق حدّاً | غير متحقق | **غير متحقق** |
| `TestP03ExcludedFileChangedBetweenReadAndPublishRefused` | لم يُشغَّل | — | `verifyUnchanged` موجود مسبقاً ⇒ حارس | غير متحقق | **غير متحقق** |
| `TestP04ExcludedWriteJournalsIntent` | لم يُشغَّل | — | HEAD يسجّل blob ids حقيقية بدل الحارس | غير متحقق | **غير متحقق** |
| `TestP04ExcludedWriteRefusedWhenIntentJournalingFails` | لم يُشغَّل | — | `OnMutationPrepared` مُستدعى مسبقاً ⇒ حارس | غير متحقق | **غير متحقق** |
| `TestP01SymlinkToExcludedTargetIsRefused` | لم يُشغَّل | — | `safefs` يرفض الروابط أصلاً ⇒ حارس | غير متحقق | **غير متحقق** |

لا يوجد أي `SKIP` — لا اختبار نُفِّذ أصلاً. **صفر تصنيف RED مثبت.**

---

## 7. الخطوة 6 — الرابط الرمزي في الأساس: **مُثبت في الشفرة**

| السطر | النص |
|---|---|
| `internal/safefs/open_unix.go:24` | `const openFlagsDir = unix.O_RDONLY \| unix.O_CLOEXEC \| unix.O_NOFOLLOW \| unix.O_NONBLOCK \| unix.O_DIRECTORY` |
| `internal/safefs/open_unix.go:30` | `const openFlagsFile = unix.O_RDONLY \| unix.O_CLOEXEC \| unix.O_NOFOLLOW \| unix.O_NONBLOCK` |
| `internal/safefs/open_unix.go:121-122` | `if errors.Is(err, unix.ELOOP) { return fmt.Errorf("%w: %s", ErrSymlink, path) }` |
| `internal/safefs/open_unix.go:133` | `if errors.Is(err, unix.ELOOP) \|\| errors.Is(err, unix.ENOTDIR) { ... ErrNotDirectory ... }` |
| `internal/safefs/errors.go:42` | `ErrSymlink = errors.New("safefs: final path component is a symlink")` |

**حالة `TestP01SymlinkToExcludedTargetIsRefused`: لم يُشغَّل** (لا PASS ولا FAIL ولا SKIP). السطر الذي يثبت السبب ساكناً هو `open_unix.go:24` و`:30` (`O_NOFOLLOW`) مع `:121-122` (تحويل `ELOOP` إلى `ErrSymlink`). **دليل ساكن فقط، لا يرقى إلى تصنيف «حارس» مختبَر.**

---

## 8. الخطوة 7 — تعديلات التعليقات (بالأسطر)

عُدِّلت **تعليقات فقط**؛ لم يُمس منطق أي اختبار. القاعدة: الفشل **غير مثبت** (الترجمة نفسها فاشلة)، فلا يجوز وسم أي اختبار بـ«RED صالح».

| الملف | السطر | قبل | بعد |
|---|---|---|---|
| `internal/tools/p0_enforcement_test.go` | 51 | `— RED (valid).` | `— RED متوقع (غير مثبت: لم يُشغَّل).` |
| `internal/tools/p0_enforcement_test.go` | 77 | `— RED (valid).` | `— RED متوقع (غير مثبت: لم يُشغَّل).` |
| `internal/tools/p0_enforcement_test.go` | 137 | `— RED (valid).` | `— RED متوقع (غير مثبت: لم يُشغَّل).` |
| `internal/tools/p0_undo_red_test.go` | 32 | `— RED (valid).` | `— RED متوقع (غير مثبت: لم يُشغَّل).` |
| `internal/tools/p0_undo_red_test.go` | 69 | `— RED (valid).` | `— RED متوقع (غير مثبت: لم يُشغَّل).` |
| `internal/tools/p0_undo_red_test.go` | 125 | `— RED (valid).` | `— RED متوقع (غير مثبت: لم يُشغَّل).` |
| `internal/tools/p0_enforcement_test.go` | رأس الملف (14-42) | «Compile status (VERIFIED) … exit 0» | «Compile status (FAILS on the sanctioned base — CORRECTED)» مع نص الخطأ الحرفي وتصحيح الادعاء |
| `internal/tools/p0_undo_red_test.go` | رأس الملف (15-17) | «Compile status (VERIFIED) …» | «Compile status (FAILS on the sanctioned base — CORRECTED)» |

**تصحيح جوهري:** ادعاء سابق في رأس `p0_enforcement_test.go` بأن الاختبارات «تُترجم بنجاح على كود ما قبل الإصلاح مع السقالة» **مسحوب** — فهو يغفل اعتماد `nabd/internal/approval`. السجل الحرفي للفشل مُدرج في الرأس الآن.

**التحقق:** `gofmt -l internal/tools/` نظيف؛ `GOOS=linux go vet ./internal/tools/` في شجرة العمل = 0.

---

## 9. ما لم يُتحقق منه وسببه

1. **لا تشغيل لأي اختبار** — Linux غير متاح (شرط توقف 1).
2. **الترجمة على الأساس فشلت** — اعتماد غير معلن على `nabd/internal/approval` (شرط توقف 3).
3. **«السقالة لا تغيّر السلوك»** — يحتاج تشغيلاً قبل/بعد، وهو متعذّر.
4. **الخطوة 4 بالكامل** (السجلّان `red-base-9f89de8.log` و`full-base-9f89de8.log`) — لم تُنتَج.
5. **جدول الخطوة 5** — كله «غير متحقق».
6. **`TestP05*`** — نفس المصير.
7. **ادعاء `docs/THREAT_MODEL.md:75`** («GUARANTEED») لم يُختبر ضد هذه الاختبارات.

---

## 10. تصريح

**لم يُغلق أي بند.** لم يُشغَّل أي اختبار، ولم يُصنَّف أي اختبار كـRED، ولم يُعدَّل أي منطق اختبار، ولم يُدفع أي شيء إلى أي مستودع. التعديلات الوحيدة: السقالة (التزام محلي `47ff719`) وتعليقات الخطوة 7.

### الأوامر الجاهزة لتشغيلها على Linux (§5، البديل 1)

```bash
# 1) شجرة أساس نظيفة عند الـSHA
git clone https://github.com/amiraq1/Nabd.git nabd-verify && cd nabd-verify
git fetch origin
git worktree add --detach /tmp/nabd-base origin/ci/threat-model-gate-covers-display
cd /tmp/nabd-base && git rev-parse HEAD   # يجب أن يطابق 9f89de824e8ed3cd9186f8a37781d570c6ca69d2

# 2) انسخ الاختبارات + protected.go + حزمة approval (الاعتماد الناقص)
cp <workspace>/internal/tools/{p0_2_test.go,p0_3_test.go,p0_4_test.go,p0_enforcement_test.go,p0_undo_red_test.go,protected.go} internal/tools/
cp -r <workspace>/internal/approval internal/

# 3) السقالة (نفس المحتوى في §3) ثم
go test ./internal/tools/ -count=1        # قبل إضافة الاختبارات
# 4) أضف الاختبارات ثم
GOOS=linux GOARCH=amd64 go vet ./internal/tools/
GOOS=linux GOARCH=amd64 go test -c -o /dev/null ./internal/tools/
go test -run 'TestP01CommitRefusesNilRegistry|TestP03ExcludedFileOverLimitRefused|TestP03ExcludedFileChangedBetweenReadAndPublishRefused|TestP04ExcludedWriteJournalsIntent|TestP04ExcludedWriteRefusedWhenIntentJournalingFails|TestP01SymlinkToExcludedTargetIsRefused' -count=1 -v ./internal/tools/ 2>&1 | tee red-base.log
go test ./internal/tools/ -count=1 2>&1 | tee full-base.log
```

**ملاحظة على الخطوة 2:** نسخ `internal/approval/` يعني إدخال كود إنتاج من الإصلاح إلى الأساس. هذا قرار منهجي يحتاج حسمك: إمّا قبوله (والأساس لم يعد نقيّاً)، أو إعادة كتابة `approvedCtx` لتستغني عن الحزمة (وهذا تعديل اختبار ممنوع بقاعدتك 2).
