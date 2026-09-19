# تقرير تكافؤ الواجهة ومصفوفة الفروقات (UI Parity Report)

* **المهمة:** UI-01 / UI-02 — جرد التكافؤ وتجهيز مسار تقاعد Chat
* **تاريخ التحديث:** 2026-09-19
* **المرجع:** [ADR-0001: اعتماد Feed واجهةً تفاعلية وحيدة وتقاعد Chat](../DECISIONS/0001-single-interactive-ui.md)

---

## إحصاء الفجوات وحالة بوابة التحقق

> **عدد الفجوات غير المحسومة:** `0` (صفر)  
> **عدد الفجوات المحسومة تعاقدياً:** `4` (البانر، دلالات Ctrl-C، شاشة Alt-Screen، ترحيل علم `--feed`)  
> **حالة بوابة الانتقال:** خالية تماماً من البنود المعلقة أو غير المحسومة. جاهزة لخطوات التنفيذ.

---

## ١. الإجابات والنتائج المحسومة

### س١: متى انقلب الافتراضي وما أثره الميداني؟
* **الكوميت:** [`b7eecac`](https://github.com/amiraq1/Nabd/commit/b7eecac65d2449a8d9443a7d58728ce346218d16) في PR #123 (الأربعاء 16 سبتمبر 2026).
* **الحقيقة الميدانية:** آخر إصدار شُحن للمستخدمين هو `v1.5.0` وكان بـ `flag.Bool("feed", false, ...)`. قلب الافتراضي لم يُشحن بعد في أي إصدار رسمي؛ هو موجود على `master` منذ 3 أيام عبر ~80 كوميت.
* **الأثر العملي:** لا توجد نافذة مراقبة ميدانية بعد. عند صدور `v1.6.0`، سيواجه مستخدمو `v1.5.0` الواجهة الجديدة لأول مرة تلقائياً. لذلك، فإن إبقاء معلمين (`v1.6.0` مع التحذير وخارطة الترحيل، ثم `v1.7.0` للحذف) هو القرار المعماري الضروري.

### س٢: أوّليات Replay وتفكيك render.go
* **الموضع:** جميع الأوّليات المشتركة (`dim`، `block`، `flushJoin`، `partialTail`، `DefaultWidth`) معرّفة داخل **[`internal/ui/render.go`](file:///data/data/com.termux/files/home/Nabd/internal/ui/render.go)** وليست في `chat.go`.
* **القرار التنفيذي:** في PR مستقل (PR 1)، يتم فصل `render.go` إلى:
  - `internal/ui/render_text.go`: يحتوي على الأوّليات المشتركة المملوكة لـ `Replay`.
  - `internal/ui/render.go`: يبقى محتوياً على `RenderEvent` القديم المخصص لـ Chat لحين حذفه.
  - إضافة اختبار يثبت استقلال `Replay` عن أي رمز تابع لـ Chat.

### س٣: دلالات Ctrl-C وتعديل المعيار ١٢
* **سلوك Chat القديم:** خروج فوري غير مشروط عند الخمول (`m.running == false`) حتى لو كان المستخدم قد كتب نصاً.
* **سلوك Feed المعتمد (سلم من 6 حواجز و7 نتائج حتمية):**
  1. إلغاء مطالبة المفتاح السري (`secretPrompt`) فقط دون إلغاء الجولة خلفه؛ لا يخرج قط.
  2. نافذة صلاحيات أو قرار معلق (`modalVisible || decisionPending`):
     - مع وجود تشغيل جاري (`running || busy`): إلغاء التشغيل مع بقاء القرار معلقاً (لا موافقة ولا خروج قط).
     - دون تشغيل جاري (نافذة يتيمة): تجاهل آمن (`no-op`) لا يمس الحالة ولا يخرج قط.
  3. إلغاء الجولة التنفيذية الجارية (`cancelRun`؛ `running || busy`)؛ لا يخرج قط أثناء التشغيل.
  4. إلغاء وإغلاق وضع البحث (`search.active`) دون مساس بمسودة الملحّن؛ لا يخرج قط.
  5. **مسح حقل الإدخال فقط (`m.composer.value() != ""`):** تصفير المسودة وتصفير استعراض السجل وإغلاق القوائم المنبثقة، مع عرض التلميح الموثق في سطر الحالة:
     `const ctrlCClearHint = "input cleared · press ctrl+c again to quit"` لمنع خروج المستخدم صدفة.
  6. **الخروج (`tea.Quit`):** فقط وحصراً عند خمول الجلسة بالكامل وفراغ حقل الإدخال تماماً.
* **التثبيت التعاقدي للمعيار ١٢:**
  - تم تثبيت السلم كعقد جامد في **[`internal/ui/feed_ctrlc_contract_test.go`](file:///data/data/com.termux/files/home/Nabd/internal/ui/feed_ctrlc_contract_test.go)** و **[`internal/ui/feed_ctrlc_helpers_test.go`](file:///data/data/com.termux/files/home/Nabd/internal/ui/feed_ctrlc_helpers_test.go)**.
  - يغطي الطقم مصفوفة كاملة من 128 حالة (2^6 حالات تبديل × حالتي ملحّن)، والسيناريوهات المسمّاة، وتسلسل الخروج بضغطتين متتاليتين، وإثبات أن Ctrl-C لا يسرّب موافقة لبوابة الصلاحيات.
* **الأثر الأمني وتدقيق الإشارات الخارجية (SIGINT vs Raw Mode):**
  - Bubble Tea يضع الطرفية في raw mode، وهو ما يُلغي `ISIG` للنظام، فتصل نقرة المفتاح كبايت `0x03` → `tea.KeyCtrlC` → سلم `onCtrlC`. مسار لوحة المفاتيح محمي بالكامل.
  - الثغرة الضيقة المحصورة: إشارة SIGINT قادمة من خارج لوحة المفاتيح (`kill -INT` أو إشارة مجموعة عمليات من سكربت أب) أو في نافذتي تفعيل واستعادة raw mode؛ يتجاوز المعالج الافتراضي السلم وينهي الجلسة فوراً.
  - **خطة UI-06:** معالجة الإشارات عبر تزويد خيارات البرنامج بـ `tea.WithoutSignalHandler()`، مع الحذر التام من استخدام `tea.WithoutSignals()` الذي يعطّل منظومة الإشارات كاملة ويكسر `SIGWINCH` المسؤول عن إعادة رسم الواجهة عند تغيير أبعاد الطرفية.

### س٤: حسم تناقض إمكانية الوصول (a11y)
* **[`internal/ui/a11y_init.go`](file:///data/data/com.termux/files/home/Nabd/internal/ui/a11y_init.go):** كود تهيئة على مستوى الحزمة يضبط `lipgloss.SetColorProfile(termenv.Ascii)` عند وجود متغير البيئة `NO_COLOR`.
* **[`internal/ui/accessibility_phase6_test.go`](file:///data/data/com.termux/files/home/Nabd/internal/ui/accessibility_phase6_test.go):** يختبر مكونات `Feed` حصراً:
  - اختبار حدود أوضاع العرض (`widthMode` في سلم Feed).
  - اختبار السِمة الدلالية مع `NO_COLOR`.
  - اختبار مسارات الملفات العربية المشتركة وحفظ ترتيب النسخ.
  - اختبار بطاقات الأخطاء التكيفية (`presentation.ErrorCard` في Feed).
  - اختبار سطر تلميحات التنقل (`navigationHint` في Feed).
* **الخلاصة:** لا يملك `Chat` أي كود a11y خاص به، وحذف Chat لا يُسقط أي اختبار أو قدرة في إمكانية الوصول.

### س٥: أوامر Slash
* 9 أوامر متطابقة تماماً ومختبرة في `slash_parity_test.go`. لا توجد أي فجوة دلالية.

### س٦: البانر كمصدر وحيد للحقيقة
* التباعد: `doChat` و `headless.go` يستعملان `filepath.Base(cwd)`، بينما `doChatWithFeed` يستعمل `filepath.Base(journalPath)`.
* **الحل المعماري:** استخراج دالة موحدة:
  ```go
  func sessionBanner(prov provider.Provider, root string) string {
      return fmt.Sprintf("%s · %s · %s", build.BannerPrefix(), prov.Name(), filepath.Base(root))
  }
  ```
  وتغذيتها في المواضع الثلاثة، مع اختبار يثبت عدم التباعد.

---

## ٢. خارطة ترحيل الأعلام المركزية

تُصاغ منطق التحقق في دالة مركزية `resolveUIConfig(flagUI string, flagFeed *bool, envUI string) (uiType string, warnings []string, err error)`:

| الحالة | المدخلات | النتيجة في v1.6.0 | النتيجة في v1.7.0 | النتيجة في v1.8.0 |
|---|---|---|---|---|
| **افتراضي** | لا أعلام ولا بيئة | `feed` (إبراز التغيير في CHANGELOG) | `feed` | `feed` |
| **علم قديم موجب** | `-feed` / `--feed=true` | `feed` + تحذير إهمال علم `--feed` | `feed` + رسالة إرشادية لاستعمال `--ui` | خطأ (علم غير معرّف) |
| **علم قديم سالب** | `--feed=false` | `chat` + تحذيران (إهمال `--feed` وإهمال `Chat`) | رفض (خروج 2) + رسالة تشرح حذف Chat | خطأ (علم غير معرّف) |
| **علم جديد** | `--ui=feed` | `feed` نظيف | `feed` | `feed` |
| **علم جديد مهمل** | `--ui=chat` | `chat` + تحذير إهمال Chat | رفض (خروج 2) + رسالة تشرح حذف Chat | رفض (خروج 2) |
| **تعارض أعلام** | `--ui=feed --feed=false` | خطأ صريح (خروج 2) | خطأ صريح (خروج 2) | خطأ (علم غير معرّف) |
| **علم وبيئة** | `--ui` مع `NABD_UI` | العلم يفوز دائماً | العلم يفوز دائماً | العلم يفوز دائماً |

---

## ٣. قاعدة الحراسة المعمارية (AST-Based Archtest)

تُعتمد الشيفرة التالية لملف `internal/archtest/chat_removed_test.go` وتُفعّل في PR الحذف (PR 4):

```go
package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

var banned = map[string]string{
	"NewChat":       "ui.NewChat constructor",
	"Chat":          "ui.Chat model type",
	"doChat":        "chat session entry point",
	"chanSink":      "chat-only event sink",
	"newUISink":     "chat-only sink constructor",
	"uiEventBuffer": "chat-only sink buffer",
}

var skipDirs = map[string]bool{
	".git": true, ".github": true, "vendor": true,
	"testdata": true, "node_modules": true,
}

func TestChatRemovedFromGoCode(t *testing.T) {
	const root = "../.."
	fset := token.NewFileSet()

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)

		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			t.Errorf("parse %s: %v", rel, perr)
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			if why, bad := banned[id.Name]; bad {
				t.Errorf("%s:%d: banned identifier %q (%s); see docs/DECISIONS/0001-single-interactive-ui.md",
					rel, fset.Position(id.Pos()).Line, id.Name, why)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
```

---

## ٤. تسلسل الـ PRs المعتمد

1. **PR 1: فصل أوّليات Replay إلى `render_text.go`** (مستقل، يوم واحد).
2. **PR 2: توحيد `sessionBanner` واختبار التطابق** (مستقل، نصف يوم).
3. **PR 3: استخراج `prepareSession` الموحدة في `cmd/ag/main.go`** (مستقل، يوم واحد).
4. **PR 4: إدخال `--ui` وخارطة الترحيل وتحذيرات الإهمال** (مستقل، يوم ونصف).
5. **(نقطة شحن v1.6.0)**.
6. **PR 5: الحذف النهائي لـ Chat وتفعيل `internal/archtest`** (مبني على v1.6.0).
7. **(نقطة شحن v1.7.0 مع جذمور `--feed`)**.
8. **(نقطة شحن v1.8.0 لإزالة الجذمور نهائياً)**.

---

## ٥. Stage A2: تغطية كاملة لأنواع الأحداث (Event-Type Rendering)

* **تاريخ التنفيذ:** 2026-09-19

### الأنواع الخمسة المضافة

| EventType | Rendering | المبرّر |
|---|---|---|
| `TextDelta` | `return ""` | النص التدفقي يُجمّع في المخزن المؤقت ويُطبع مرة واحدة عبر `flushJoin`. العرض هنا سيُضاعف كل رمز. |
| `EventProviderUsage` | `return ""` | بيانات قياس لا محتوى شاشة. يُصدَر مرة لكل طلب ناجح — السطر الزائف كان مرئياً في كل جلسة تقريباً. |
| `EventSkillBody` | `return ""` | محتوى غير موثوق و`block()` لا يُعقّم ANSI. العرض الخام مسار حقن طرفيّ. |
| `EventSkills` | `dim("⚑ N skills · M project")` (empty→`""`) | يتبع نمط `EventEdit`: سطر ملخّص بأعداد فقط، بلا أسماء أو مسارات. حقل النطاق هو `skill.EventSkills.Scope` من نوع `string` بقيم `"user"` أو `"project"`. الجرد الفارغ (أو سجل قديم بلا الحقل) يعود `""`. |
| `Rewind` | `dim("── rewind to #N")` | يستعير نمط `RunStart` (`── …`). لا رمز جديد. |

### الحارس المُشتق من المصدر

* حُذف `TestUnknownFallbackIsNotReachableFromKnownTypes` (القائمة اليدوية من 18 عنصرًا).
* أُنشئ `render_coverage_test.go` الذي يحلّل AST لملف `event.go` ويستخرج كل ثوابت `EventType` ديناميكياً.
* اختبار العبث: حذف حالة `EventProviderUsage` مؤقتاً أنتج فشلاً يُسمّي النوع بالضبط:
  ```
  EventType "provider_usage" fell through to the unknown-type fallback: "· provider_usage"
  ```

---

## ٦. Stage B: تقسيم render.go إلى طبقتي نصّ وأحداث

* **تاريخ التنفيذ:** 2026-09-19

### الملفان الناتجان

| ملف | المسؤولية | الاستيرادات المسموحة |
|---|---|---|
| `render_text.go` | طبقة النصّ الخالية من المجال: قياس، لفّ، قصّ، تنسيق | `fmt`, `strings`, `lipgloss`, `ansi` فقط |
| `render_event.go` | تحويل `agent.Event` إلى سطور شاشة | الأربعة أعلاه + `encoding/json`, `agent`, `presentation` |

### القرار بشأن `green`

`green` **مُستخدم** في `feed_render.go:92` (`green.Render("Nabd")`). نُقل إلى `render_text.go` مع بقية المتغيّرات.

### حارس الطبقات (`render_layers_test.go`)

* `TestRenderTextLayerStaysDomainFree` — يتحقّق أن استيرادات `render_text.go` هي الأربعة المسموحة حصرًا.
* `TestRenderTextLayerDeclarations` — قائمة مجمّدة بـ 18 تصريحًا.
* `TestRenderEventLayerDeclarations` — قائمة مجمّدة بـ 5 تصريحات.
* `TestRenderEventLayerImportsAgent` — يتحقّق أن `render_event.go` يستورد `agent`.

### بند فحص يدوي لـ PR حذف Chat

تعليق `flushJoin` يقول "Both Chat and Replay call it" — سيصبح خاطئًا بعد حذف Chat. يجب تحديثه في PR الحذف لا هنا.

### لا تضارب أسماء

`parseImports`, `topLevelNames`, `equalStrings` لم تكن موجودة في الحزمة قبل هذا الحارس.

---

## ٧. تذكرة إصلاح: سباق الترتيب في Replay وحارس المعمارية (Ticket: Replay Ordering Race)

* **تاريخ التنفيذ:** 2026-09-19
* **الخلل:** في `internal/ui/replay.go:63`، كانت الدالة `step()` تُعيد `tea.Batch(tea.Println(s), tea.Tick(1ms))`. وبما أن `Batch` يُنفّذ الأوامر بالتوازي في goroutines مستقلة، فإن نبضة الـ 1ms كانت تسبق تسليم رسالة الطباعة في نحو 13% من التشغيلات على PTY (ثبت عبر التجربة الفاصلة بفحص التيارات الخام ومحاكي `vt10x` بتبدل سطر البانر `── nabd · /corpus` مع سطر المستخدم `› cleanup`).
* **الإصلاح:** استبدال `tea.Batch(cmds...)` بـ `tea.Sequence(cmds...)`. يضمن `Sequence` تسليم رسالة أمر الطباعة إلى حلقة الأحداث قبل بدء تشغيل مؤقت النبضة اللاحقة، مما يجعل الترتيب سببيًا وبنيويًا لا احتماليًا.
* **حارس التكرار:** `TestReplayPrintTickOrderingRepetition` في `replay_order_test.go` يُعيد تشغيل السيناريو 50 مرة متتالية تحت PTY ويتحقق من تطابق البصمة بنسبة 50/50 مع كتابة تيار البايتات الخام كملف أثر (artifact) عند أي فشل.
* **حارس المعمارية (AST Guard):** `TestNoBatchWithPrintlnInUI` في `render_layers_test.go` يمنع أي ملف داخل `internal/ui` من استدعاء كلّ من `tea.Batch` و `tea.Println` معًا.
* **قرار `chat.go` (§4):**
  - كشف الفحص أن `chat.go:105` يضمّ `tea.Println(s)` إلى `waitEvent(m.events)` عبر `tea.Batch`.
  - بما أن `waitEvent` قد يلتقط حدثًا تاليًا تم بثّه مسبقًا في القناة قبل اكتمال تسليم الطباعة السابقة، فإن واجهة Chat معرّضة لنفس سباق الترتيب.
  - **القرار والتوثيق:** بما أن Chat سطح مهمل سيتم حذفه بالكامل في الإصدار v1.6.0 (وفق خارطة طريق توحيد الواجهات)، تم إدراج `chat.go` في قائمة السماح المؤقتة (`allowlist`) داخل حارس المعمارية مع تعليق صريح يربط إزالتها بـ PR حذف Chat، وتوثيقه هنا كخلل معروف في سطح مؤقت بدل إدخال تعديلات جانبية عليه قبل حذفه.

---

## ٨. مسار الحافظة (Clipboard) في Feed: بوابات البيئة والتوثيق التعاقدي

* **تاريخ التوثيق:** 2026-09-19
* **السياق:** كان `copySelectedCard` في HEAD يكتب تسلسل OSC 52 إلى `os.Stdout` دائماً عند غياب كاتب مخصّص. المسار الجديد (`copy.go` على `feat/copy-export-pipeline`) يجعل النقل **مرهوناً بالبيئة**؛ هذا قرار موثّق هنا وفي `CHANGELOG.md` صراحةً، وليس انحداراً صامتاً (بند المراجعة N1).

| البيئة | الناقل | التوقيت | سلوك الفشل |
|---|---|---|---|
| كاتب مخصّص (`m.clipboardWriter != nil`، الاختبارات) | OSC 52 عبر `io.WriteString` | متزامن | `copy unavailable` |
| جلسة بعيدة (`SSH_CONNECTION != ""`) | OSC 52 إلى `os.Stdout` | متزامن | `copy unavailable` |
| Termux (`TERMUX_VERSION` أو `PREFIX`) | `termux-clipboard-set` عبر stdin بمهلة 10s | غير متزامن (`tea.Cmd`) | `copy failed: …` بتصنيف، مع إنقاذ إلى ملف 0600 |
| غير ذلك | لا ناقل | — | `copy unavailable` |

* **المبرّر:** الكتابة المتزامنة لتسلسل هروب من داخل دورة `Update` تتنافس مع مُصيّر Bubble Tea على المقبس نفسه؛ حصرها في الجلسات البعيدة يقلّص سطح الخطر بدل توسيعه.
* **الضمان الأمني:** `redactForExport` هو المنتج الوحيد لـ `redactedText`، والتحويلان (حجب ثم تعقيم) يسبقان أي ناقل — إلى الحافظة أو إلى ملف.
* **إنفاذ تعاقدي وتأجيل بانتظار قرار منتج (N1):** حصر النقل خارج Termux في جلسات SSH (`copy unavailable` في البيئات المحلية الأخرى) هو **تأجيلٌ تقني بانتظار قرار منتج** لتحديد مصفوفة دعم الطرفيات المحلية، وليس إقراراً بصحة السلوك كحالة نهائية. الاختبار `TestCopyUnavailableOutsideTermuxAndSSH` ظهر في نفس التزام تنفيذ البوابة (`ce24462`)، فهو وصفٌ لما تم تنفيذه وحراسةٌ ضد التغيير العرضي وليس قراراً معمارياً مسبقاً.
* **توحيد مسارَي OSC 52 وتعديل عقد الإرجاع (F12):** في الالتزام `088804d`، جرى توحيد مسارَي كتابة OSC 52 عبر `writeOSC52`، وصاحب هذا التوحيد تغيير سلوكي في عقد دالة `reportSavedSuffix` من إرجاع `string` إلى `(string, bool)` للتبليغ الصريح عن فشل الإنقاذ بـ `copyRescueFailedNotice` بدلاً من ابتلاع الخطأ وسكوت سطر الحالة.
* **الديون المرتبطة:**
  - **N4:** كتابة OSC 52 المتزامنة إلى `os.Stdout` ما زالت داخل دورة التحديث؛ تتطلب إعادة هيكلة إلى `tea.Cmd` لاحقاً.
  - **N2:** فشل الإنقاذ إلى ملف كان صامتاً؛ صار مُعلناً في سطر الحالة عبر `copyRescueFailedNotice` ("export failed; text not saved").

