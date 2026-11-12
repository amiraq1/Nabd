# NabdCoder

وكيل برمجة متخصص في مشروع Nabd، محسّن لبيئة Termux.

## المميزات الجديدة
- **edit_file**: تعديل آمن بـ Search/Replace + فحص syntax لملفات Go قبل الحفظ.
- **go_verify**: تشغيل `go test` و`go vet` مع قيود ذاكرة (`GOMEMLIMIT`, `GOGC`, `-p 2`) مناسبة لأندرويد.
- **Workspace Jail**: منع تعديل ملفات خارج مجلد العمل.
- **Approval Gate**: تأكيد يدوي قبل أي تعديل.
- هيكل نظيف: `cmd/agent` + `internal/tools`.

## البناء في Termux

```bash
pkg install golang git
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
| go_verify    | go test + vet مع قيود ذاكرة                | لا     |
| list_dir     | عرض محتويات مجلد                           | لا     |

## ملاحظات
- النسخة الحالية تحتوي على stub بسيط لاستدعاء LLM (يُستبدل بعميل OpenAI-compatible حقيقي).
- صُمم للعمل داخل مجلد مشروع Nabd.
