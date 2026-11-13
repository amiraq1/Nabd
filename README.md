# NabdCoder

وكيل برمجة متخصص في مشروع Nabd، محسّن لبيئة Termux.

## المميزات الجديدة
- **edit_file**: تعديل آمن بـ Search/Replace + فحص syntax لملفات Go قبل الحفظ.
- **go_verify**: تشغيل `go test` و`go vet` مع قيود ذاكرة (`GOMEMLIMIT`, `GOGC`, `-p 2`) مناسبة لأندرويد.
- **Workspace Jail**: منع تعديل ملفات خارج مجلد العمل.
- **Approval Gate**: تأكيد يدوي يعرض المسار وdiff وSHA-256 قبل أي تعديل.
- **LLM client**: واجهة قابلة للاختبار + تنفيذ OpenAI-compatible مع retries وإلغاء فوري.
- **Shadow Storage**: نسخة قبل كل تعديل + `undo [path]` للاستعادة.
- هيكل نظيف: `cmd/agent` + `internal/tools`.

## البناء في Termux

```bash
pkg install golang git
go version  # يجب أن يكون 1.25.5 أو أحدث
git clone https://github.com/amiraq1/nabd.git
cd nabd
go mod tidy
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $PREFIX/bin/nabdcoder ./cmd/agent
nabdcoder
```

## الأدوات المتاحة
| الأداة       | الوصف                                      | تأكيد؟ |
|--------------|--------------------------------------------|--------|
| read_file    | قراءة ملف                                  | لا     |
| edit_file    | استبدال نص دقيق + فحص syntax               | نعم    |
| go_verify    | go test + vet مع قيود ذاكرة                | نعم    |
| list_dir     | عرض محتويات مجلد                           | لا     |
| mcp_call     | استدعاء أداة على خادم MCP خارجي            | نعم    |

## ملاحظات
- الإعداد من البيئة فقط: `NABD_BASE_URL`, `NABD_MODEL`, `NABD_API_KEY`.
- المسارات الحساسة (`.env`, `.git/`, `.nabd/`, `id_*`, `*.pem`, `*.key`) مرفوضة لأدوات الملفات.
- `cmd/mcp-server` للقراءة فقط: يرفض `edit_file` و`go_verify` لغياب approver، و`mcp_call` معطّل فيه.
- `go_verify` يمرر بيئة محدودة (allowlist) دون مفاتيح API، ويستخدم `GOFLAGS=-mod=readonly` و`GOPROXY=off` و`GOTOOLCHAIN=local`.
- Audit log: كل `requested`/`approved`/`denied`/`applied`/`failed` يُضاف إلى `.nabd/audit.log` (JSONL) مع SHA-256 للوسائط والنتيجة فقط، بلا محتوى ملفات؛ والكتابة إلزامية للأدوات الكاتبة/المنفذة.
- موافقة الـTUI تُصدر token أحادي الاستخدام مرتبط بالأداة وhash الوسائط وينتهي بعد 5 دقائق؛ لا يمكن تنفيذ أداة كاتبة بدونه.
- عميل LLM: `llm.Client` قابل للاختبار، وتنفيذ OpenAI-compatible من `NABD_*`، مع مهلة كلية وإلغاء فوري عند Ctrl+C.
- retries على 429/5xx/أخطاء الشبكة فقط (حد 4 محاولات، backoff+jitter، واحترام `Retry-After`).
- حد سياق تقديري 24000 tokens مع إسقاط أقدم الوحدات (system + آخر وحدة دائماً)، وحد 10 استجابات لكل دور، وكشف الاستدعاءات المتكررة.
- مهلة `go_verify` (90s) تقتل شجرة العمليات كاملة، والنتيجة منظمة: `PASS`/`FAIL`، أول 5 إخفاقات، وذيل الإخراج.
- أنماط حزم `go_verify` يجب أن تكون workspace-relative مثل `./...` أو `./pkg`؛ المسارات الخارجية و`std` مرفوضة.
- التعديل ذرّي (temp + fsync + rename) ويحافظ على صلاحيات الملف.
- Shadow Storage: نسخة قبل كل تعديل في `.nabd/shadow` (آخر 20 نسخة لكل ملف أو 7 أيام)، و`undo [path]` في الـTUI يستعيد آخر تعديل إن لم يتغيّر الملف.
- الكتابة الذرّية تعتمد على rename: إذا كان للملف hard link آخر، فسيحتفظ الاسم الآخر بالمحتوى القديم.
- تاريخ undo محدود: آخر 20 نسخة لكل مسار أو 7 أيام، ثم تُحذف الأقدم تلقائياً (سياسة ثابتة حالياً، بلا إعداد).
- حدود الحجم: قراءة 512KiB، قائمة 1000 مدخل، تعديل 2MiB؛ وتعديل symlink مرفوض.
- صُمم للعمل داخل مجلد مشروع Nabd.
