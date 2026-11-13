# NabdCoder

وكيل برمجة متخصص في مشروع Nabd، محسّن لبيئة Termux.

## المميزات الجديدة
- **edit_file**: تعديل آمن بـ Search/Replace + فحص syntax لملفات Go قبل الحفظ.
- **go_verify**: تشغيل `go test` و`go vet` مع قيود ذاكرة (`GOMEMLIMIT`, `GOGC`, `-p 2`) مناسبة لأندرويد.
- **Workspace Jail**: منع تعديل ملفات خارج مجلد العمل.
- **Approval Gate**: تأكيد يدوي يعرض المسار وdiff وSHA-256 قبل أي تعديل.
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
- `go_verify` يمرر بيئة محدودة (allowlist) دون مفاتيح API، ويستخدم `GOFLAGS=-mod=readonly`.
- صُمم للعمل داخل مجلد مشروع Nabd.
