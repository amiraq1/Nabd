# تقرير تنفيذ ميزة Native Multi-Provider Router (v1.2.0)

هذا التقرير هو التوثيق المرجعي الشامل لتنفيذ ميزة **Native Multi-Provider Router** في مشروع `nabd` والمقترحة للإصدار v1.2.0.

---

## 1. ملخص سلسلة الـ Commits والمراحل

- **Baseline Commit (B):** `7323b9272e138641b6db5537d6dc5a816826bf9d` (tag: v1.1.0)
- **Branch:** `feature/native-provider-router`
- **P1 Commit:** `bb2573d` — *feat: parse and validate explicit NABD_ROUTES*
  - تحليل إعدادات المسارات، التحقق الصارم، القواعد النحوية، وضبط بيئة CI.
- **P2 Commit:** `fcebcdb` — *refactor: expose typed single-attempt provider failures + redaction*
  - إدخال `RetryPolicy` (`RetryStandalone`, `RetrySingleAttempt`)، منع التكرار التلقائي في نمط الراوتر، التنقيح الصارم للأسرار (`Redact`, `SanitizeBody`).
- **P3 Commit:** `fe88e96` — *feat: add deterministic pre-output provider router*
  - محرك الراوتر الكامل، الالتزام الحتمي (Commit Linearization)، التراجع الآمن قبل الإخراج فقط، إقرار التنظيف (Cleanup ACK)، ومعالجة 429 و Retry-After (RFC 9110).
- **P4 Commit:** *(الحالي — انظر أسفل التقرير للـ hash)* — *feat: journal sanitized provider routing decisions*
  - تسجيل أحداث التوجيه `provider_route` في الـ journal، عزلها عن رسائل النموذج، التوثيق في `README.md`، وهذا التقرير.

---

## 2. القواعد النحوية والعقود (Grammar & Contracts)

### 2.1 القواعد النحوية (F-section Grammar)
- **الصيغة القانونية:**
  ```text
  routes   = entry ("," entry)*
  entry    = provider ":" model
  provider = 1..32 bytes, trimmed, lowercased, allow-listed
  model    = 1..256 bytes, trimmed, case-preserved, may contain ':'
  ```
- **المزودون المسموح بهم فقط (AllowedProviders):** `anthropic`, `groq`, `openrouter`, `nvidia`.
- **الفصل عند أول `:`:** ما قبل النقطتين الأوليين هو اسم المزود، وما بعده هو اسم النموذج بالكامل حتى الفاصلة.
- **السماح باحتواء `:` في النموذج:** مثل `openrouter:anthropic/claude-3.5-haiku:beta` أو `openrouter:model-b:free`.
- **الحدود:** عدد المسارات بين 1 و 16 مسارًا.
- **سياسة التكرار:** يُرفض التكرار للزوج المتطابق تمامًا (`provider:model`). يُسمح للمزود نفسه بنماذج مختلفة.

### 2.2 عقود المتغيرات والبيئة
- `NABD_PROVIDER=router`: يُفعّل موجه المزودين.
- `NABD_ROUTER_MODE`: القيمة الوحيدة المقبولة هي `fallback`.
- `NABD_ROUTER_PRESTREAM_TIMEOUT`: مهلة بدء الاستجابة بالثواني لكل مسار مستقل (من 5 إلى 120 ثانية، افتراضيًا 30s).
- `NABD_MODEL`: يُهمل تمامًا في نمط الراوتر (مع إشعار تنبيه لمستخدم الطرفية).
- `NABD_BASE_URL`: **ممنوع في نمط الراوتر**؛ وجوده يوقف التشغيل فورًا لمنع خلط عناوين المزودين.
- **المفاتيح:** `ANTHROPIC_API_KEY`, `GROQ_API_KEY`, `OPENROUTER_API_KEY`, `NVIDIA_API_KEY`. يتم التحقق من وجود جميع المفاتيح لجميع المسارات المحددة عند بدء التشغيل، ورسائل الخطأ توضح اسم المتغير المفقود دون كشف أي سر.

---

## 3. ملكية إعادة المحاولة (Retry Ownership & Policies)

- **`RetryStandalone` (المسار المباشر القديم — v1.1.0):**
  - لم يتغير أي سلوك قديم.
  - `Anthropic` يعيد المحاولة حتى 4 مرات مع مؤقت 25 ثانية للـ first-byte.
  - `OpenAICompat` يعيد المحاولة مرة واحدة عند الأخطاء العابرة مع مؤقت 25 ثانية للـ first-byte.
- **`RetrySingleAttempt` (مسار الراوتر):**
  - يقوم المزود بمحاولة HTTP واحدة بالضبط لكل استدعاء.
  - يُعطّل المؤقت الداخلي للـ 25 ثانية (لأن مهلة الراوتر `prestreamTimeout` هي المسؤولة عن الميزانية الزمنية).
  - لا توجد إعادات محاولة داخلية أو نوم مزدوج (Double-waiting). الراوتر هو المالك الحصري لقرارات الانتقال الاحتياطي (Fallback).

---

## 4. الالتزام والتنظيف وإلغاء السياق (Commit, Timeout & Cleanup)

- **نقطة الالتزام الحتمية (Commit Linearization Point - J.1):**
  - قبل إرسال أول قطعة دلالية (`ChunkText`, `ChunkToolCall`, `ChunkStop`)، يفحص الراوتر تحت قفل محلي:
    1. هل أُلغي سياق الأب (`parentCtx.Err() != nil`)؟ إذا نعم، يفوز الإلغاء، ولا يحدث fallback.
    2. هل انتهت مهلة المسار (`clock.Now() >= routeDeadline`)؟ إذا نعم، تفوز المهلة ويحدث fallback للمسار التالي إن وجد.
    3. خلاف ذلك، يفوز الالتزام (`committed = true`).
- **الفصل بين تغيير الحالة وإرسال القناة (J.2):**
  - يتم فك القفل أولًا ثم إرسال القطع المخزنة دلاليًا عبر `select` مع `parentCtx.Done()`.
- **إقرار التنظيف (Cleanup Acknowledgment - G.3-G.5):**
  - عند إلغاء أو فشل أي مسار، ينتظر الراوتر إغلاق قناة المزود السابقة (`<-routeCh`) للتأكد من انتهاء الـ goroutines وإغلاق اتصال الـ HTTP.
  - مهلة التنظيف الثابتة: `RouteCleanupTimeout = 2s`.
  - إذا تجاوز التنظيف 2 ثانية، يتوقف الراوتر فورًا بخطأ `ErrRouteCleanupTimeout` حمايةً للموارد، ولا يُشغل أي مسار تالٍ.

---

## 5. تنقيح الأسرار وحدود الذاكرة (Redaction & Size Limits)

- **قاعدة N5 الصارمة (Redact-before-truncate):**
  - يتم تطبيق التنقيح أولًا (`RedactExactKeys` ثم `Redact`)، وفقط بعد ذلك يتم قطع النص عند الحدود القصوى (`TruncateBody`).
  - حدود الحجم:
    - محتوى الخطأ الفردي: أقصاه `4 KiB` (`maxBodyBytes = 4096`).
    - الخطأ التجميعي الشامل: أقصاه `16 KiB` (`maxAggregateBytes = 16384`).
- **أنماط المفاتيح المنقحة:**
  - `sk-ant-...`, `sk-or-...`, `gsk_...`, `nvapi-...`, و `Authorization: Bearer ...`.

---

## 6. سجل الأحداث والحدود المعمارية (Journal Schema & Boundaries)

- **الحد المعماري الصارم (Architectural Boundary - Section O):**
  - `internal/provider` **لا يستورد** `internal/agent` ولا `internal/store`.
  - الموجه يرسل بيانات تتبع منقحة كـ metadata عبر القناة (`ChunkTrace` / `ChunkRouteTrace`).
  - حلقة الوكيل (`agent.Loop`) تستقبل القطعة وتحولها إلى حدث دفتر الجلسة `EventProviderRoute`.
- **حدث `EventProviderRoute`:**
  ```json
  {
    "seq": 10,
    "t": "2026-09-05T00:00:00Z",
    "type": "provider_route",
    "route": {
      "stream_id": "0123456789abcdef0123456789abcdef",
      "provider": "groq",
      "model": "qwen-2.5-32b",
      "attempt": 1,
      "status": "selected"
    }
  }
  ```
  - الحالات: `attempted`, `failed`, `selected`, `exhausted`.
  - `stream_id`: معرّف عشوائي 128 بت (`16 bytes` مشفرة كـ hex من `crypto/rand`).
  - عزل النموذج: تم تحديث `Messages()` في `messages.go` ليتجاهل `EventProviderRoute` تمامًا، مما يضمن عدم وصول أي بيانات توجيه إلى سياق النموذج أو كسر تسلسل `tool_use / tool_result`.

---

## 7. سياسة 429 و Retry-After (RFC 9110 - Section L)

- عند مواجهة خطأ 429 قبل الالتزام، ينتقل الراوتر فورًا للمسار التالي دون أي نوم أو تأخير.
- عند استنفاد جميع المسارات بسبب 429 فقط:
  - يُصدر الراوتر قطعة `ChunkRateLimit` واحدة فقط.
  - يُحلل ترويسات `Retry-After` بصيغة الثواني وصيغة `HTTP-date` عبر الساعة المحقونة (`Clock`).
  - يتم استبعاد القيم السالبة، الصفرية، السابقة، والـ NaN.
  - يتم اختيار **أقصر قيمة موجبة صالحة** وتثبيتها عند سقف `120s` (`maxRetryCeiling`).
  - إذا لم تتوفر قيمة صالحة، تُترك القيمة صفراً ليعتمد الوكيل على التراجع الأسي الافتراضي.

---

## 8. مصفوفة الاختبارات والتحقق (Verification & Test Matrix)

### 8.1 إحصاءات الاختبارات
- اختبارات الراوتر في `internal/provider`: **77 اختبارًا**.
- اختبارات التتبع في `internal/agent`: **18 اختبارًا**.
- إجمالي اختبارات المشروع: جميع الاختبارات في كافة الحزم الـ 10 تجتاز بنجاح (`go test ./... -count=1`).

### 8.2 اختبار التكرار وعدم الـ Flake (Repeat 100)
```text
ROUTER_REPEAT_100: PASS (51.868s)
```
تم تشغيل 100 جولة متتالية لاختبارات الراوتر دون تسجيل أي فشل أو تعليق.

### 8.3 أدوات الفحص الثابت والبيئة
- **Staticcheck:** `staticcheck 2026.2.1 (0.8.1)` — يعطي نتيجة نظيفة تمامًا (PASS).
- **Check Exec Env:** `bash scripts/check-exec-env.sh` — PASS (كل أمر تنفيذي يحدد بيئته صراحة).
- **Clean Clone Verification:** تم بنجاح عبر استخراج الأرشيف وبناء واختبار الكود في بيئة معزولة.

---

## 9. حالة فاحص السباق (Race Detector Status)

```text
LOCAL_RACE: PENDING_ENVIRONMENT (غير مدعوم محليًا على Android/Termux ARM64 بدون CGO)
CI_LINUX_AMD64_RACE: REQUIRED (إلزامي عبر GitHub Actions على Linux x86_64 قبل الدمج)
MERGE_ALLOWED: NO (معلق حتى نجاح CI بالكامل)
TAG_ALLOWED: NO
RELEASE_ALLOWED: NO
```

---

## 10. القيود المعروفة الموثقة (Documented Known Limitations)

1. **`KNOWN_LIMITATION_CIRCUIT_BREAKER`:** غير منفذ في v1.2.0؛ كل طلب يبدأ من أول مسار في القائمة دون تذكر الأعطال السابقة عبر الطلبات المستقلة.
2. **`KNOWN_LIMITATION_REMOTE_CANCELLATION`:** إلغاء الاتصال محليًا يقطع التدفق المحلي، ولكن الخادم البعيد قد يستمر بالمعالجة حتى يكتشف قطع اتصال TCP.
3. **`KNOWN_LIMITATION_DOUBLE_BILLING_RACE`:** انتهاء المهلة محليًا وبدء مزود بديل قبل أن يلغي المزود الأول طلبه عن بعد قد يترتب عليه تكلفة طفيفة على كلا المزودين.
4. **`KNOWN_LIMITATION_WORST_CASE_LATENCY`:** أسوأ زمن انتظار نظري قبل الاستنفاد التام هو: `عدد المسارات × (مهلة ما قبل الإخراج + 2 ثانية تنظيف)`.
5. **`COMMA_SEPARATOR_ESCAPE`:** الفاصلة `,` هي الفاصل الوحيد ولا يمكن الهروب منها داخل اسم النموذج.
