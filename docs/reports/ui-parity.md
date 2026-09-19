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

