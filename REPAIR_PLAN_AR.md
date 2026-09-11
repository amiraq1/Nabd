# خطة إصلاح Nabd

## النتيجة التنفيذية

تم تطبيق إصلاحات الأولوية `P0` في `Compact` و`Rewind`، ثم طُبقت إصلاحات `P1` و`P2` الواردة في هذه الوثيقة. أصبح الحدث لا يدخل إلى الحالة الداخلية إلا بعد نجاح `Sink.Emit`, وأصبح خطأ التخزين يصل إلى المستدعي بدل إخفائه. أضيفت كذلك atomic session creation، وexact-key redaction، وسقف الإنفاق، ومهلة دور المزود، وcircuit breaker، وحارس provenance، وبوابة CI واختبارات هذه المسارات.

بقية البنود أدناه هي خطة تطبيقية مرتبة، مع كود مقترح متوافق مع بنية المستودع الحالية. لم تُشغّل اختبارات Go محليًا لأن `go` و`gofmt` غير مثبتين في بيئة التنفيذ الحالية.

## مصفوفة الأولويات

| الأولوية | المشكلة | الإجراء | الحالة |
|---|---|---|---|
| P0 | `Compact` يبتلع خطأ `emitLocked` | إرجاع الخطأ والتحقق من atomicity | مطبق |
| P0 | `Rewind` يبتلع خطأ `emitAt` | إرجاع الخطأ وعدم تغيير الحالة عند فشل sink | مطبق |
| P0 | تحديث الذاكرة قبل نجاح التخزين | استدعاء sink أولًا ثم commit داخلي | مطبق |
| P0 | غياب اختبارات `failSink` | اختبارات Compact وRewind | مطبق |
| P1 | تصادم أسماء الجلسات بين العمليات | `O_EXCL` مع random retry | مطبق |
| P1 | سقف `MaxTurns=40` بلا سقف إنفاق | إضافة token budget تراكمي | مطبق |
| P1 | غياب timeout مستقل لدور المزود | `context.WithTimeout` لكل attempt | مطبق |
| P1 | تكرار 401/403 عبر المسارات | circuit breaker داخل Router | مطبق |
| P2 | `exactKeys` تمرر دائمًا كـ`nil` | واجهة `SecretKeyProvider` | مطبق |
| P2 | اختبار idempotence ناقص | اختبار كامل لـ`SanitizeBody` | مطبق |
| P2 | خطأ اختبار `Close` وتعليق collision | إصلاح الاختبار والتعليق | مطبق جزئيًا؛ التعليق يحتاج تحديثًا تجميليًا |
| P2 | provenance لاستكمال الملفات | predicate بنيوي يعتمد على ToolEnd | مطبق |
| P2 | فروع مدموجة قديمة | تحقق ثم حذف الفروع فقط | لم ينفذ؛ يتطلب قرارًا تشغيليًا |

## 1. الإصلاح المطبق: `Compact` و`Rewind`

### `internal/agent/compact.go`

بدلًا من:

```go
_ = l.emitLocked(l.parent, compactEvent)
l.mu.Unlock()
return nil
```

أصبح:

```go
err := l.emitLocked(l.parent, compactEvent)
l.mu.Unlock()
return err
```

### `internal/agent/rewind.go`

بدلًا من تجاهل النتيجة:

```go
l.emitAt(cut.Parent, Event{...})
return cut.Text, nil
```

أصبح:

```go
if err := l.emitAt(cut.Parent, Event{
    Type: Rewind,
    Text: fmt.Sprintf("rewound %d turns (%d events)", n, dropped),
}); err != nil {
    return "", err
}
return cut.Text, nil
```

### جعل `emitLocked` عملية commit حقيقية

المشكلة الأصلية أن `seq`, `parent`, و`hist` تتغير قبل استدعاء الـsink. الإصلاح هو تجهيز الحدث، ثم الكتابة إلى sink، ثم تثبيت الحالة عند نجاح الكتابة:

```go
func (l *Loop) emitLocked(parent int, e Event) error {
    nextSeq := l.seq + 1
    e.Seq, e.Parent = nextSeq, parent
    if e.Time.IsZero() {
        e.Time = l.clockNowUTC()
    }

    if l.Sink != nil {
        if err := l.Sink.Emit(e); err != nil {
            return err
        }
    }

    l.seq = nextSeq
    l.parent = e.Seq
    l.hist = append(l.hist, e)
    return nil
}
```

هذا يضمن أن فشل journal لا يترك الذاكرة وكأن الحدث حُفظ. يظل عقد `Sink.Emit` هو أن عودة `nil` تعني قبول الحدث. إذا كان sink خارجيًا قد يكتب جزئيًا ثم يعيد خطأ، فيجب على sink نفسه ضمان atomic append أو إعادة فتح journal والتحقق منه.

### الاختبارات المضافة

الاختبارات في `internal/agent/persistence_failure_test.go` تثبت أن:

1. `Rewind` يعيد خطأ sink.
2. `Compact` يعيد خطأ sink.
3. لا يضاف حدث `Rewind` أو `Compact` عند الفشل.
4. لا يتغير طول التاريخ الداخلي عند الفشل.

## 2. إغلاق تصادم أسماء الجلسات باستخدام `O_EXCL`

### التصميم

لا ينبغي أن يعتمد التفرد على PID والعداد فقط. يجب أن يكون إنشاء جلسة جديدة عملية ذرية، وأن يتغير الاسم عند كل محاولة فشل.

أضف إلى `internal/store/jsonl.go`:

```go
// NewJSONLExclusive creates a new session file and fails with os.ErrExist
// if the candidate already exists. It never opens an existing journal.
func NewJSONLExclusive(path string) (*JSONL, error) {
    if err := ensurePrivateParent(filepath.Dir(path)); err != nil {
        return nil, err
    }

    f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
    if err != nil {
        return nil, err
    }
    if err := f.Chmod(0o600); err != nil {
        _ = f.Close()
        _ = os.Remove(path)
        return nil, fmt.Errorf("store: harden new journal permissions: %w", err)
    }
    return &JSONL{path: path, f: f, w: bufio.NewWriter(f)}, nil
}
```

ثم أضف منشئًا خاصًا للجلسة الجديدة في `cmd/ag/main.go`:

```go
func newSessionJournal(dir string) (*store.JSONL, string, error) {
    if dir == "" {
        var err error
        dir, err = defaultSessionDir()
        if err != nil {
            return nil, "", err
        }
    }

    const maxAttempts = 16
    for attempt := 0; attempt < maxAttempts; attempt++ {
        // Include a fresh random suffix in newSessionName, not only PID/counter.
        path := filepath.Join(dir, newSessionName(time.Now().UTC().Format("20060102-150405.000")))
        journal, err := store.NewJSONLExclusive(path)
        if err == nil {
            return journal, path, nil
        }
        if !errors.Is(err, os.ErrExist) {
            return nil, "", err
        }
    }
    return nil, "", fmt.Errorf("could not allocate unique session path after %d attempts", maxAttempts)
}
```

ولاسم عشوائي آمن، استبدل suffix الحالي بـ:

```go
func newSessionSuffix() (string, error) {
    var b [16]byte
    if _, err := cryptoRand.Read(b[:]); err != nil {
        return "", err
    }
    return fmt.Sprintf("-%x", b[:]), nil
}
```

عمليًا يجب تعديل `sessionPath` أو مسار `doChat` و`runHeadless` بحيث يستخدمان `newSessionJournal` للجلسة الجديدة، بينما يبقى `--continue` على `NewJSONL` العادي لفتح الملف الموجود.

ويجب تعديل الاختبار الذي يرسل نتيجتين عند فشل `Close` إلى:

```go
if err := j.Close(); err != nil {
    errs <- err
    return
}
errs <- nil
```

كما يجب تغيير التعليق `no Seq collision across files` إلى `no Seq collision within one file`.

## 3. تمرير المفاتيح الحقيقية إلى `SanitizeBody`

### واجهة استخراج سرّية محدودة

لا تمرر كامل كائن المزود أو إعداداته إلى sanitizer. أضف واجهة صغيرة:

```go
// SecretKeyProvider exposes only exact secret values needed for redaction.
type SecretKeyProvider interface {
    SecretKeys() []string
}
```

طبّقها على العملاء الذين يملكون `Key`:

```go
func (a *Anthropic) SecretKeys() []string {
    if a == nil || a.Key == "" {
        return nil
    }
    return []string{a.Key}
}

func (o *OpenAICompat) SecretKeys() []string {
    if o == nil || o.Key == "" {
        return nil
    }
    return []string{o.Key}
}
```

أضف helper داخل `router.go`:

```go
func exactKeys(client SingleAttempt) []string {
    if p, ok := client.(SecretKeyProvider); ok {
        return p.SecretKeys()
    }
    return nil
}
```

ثم استبدل استدعاءات:

```go
SanitizeBody(body, nil)
```

بـ:

```go
SanitizeBody(body, exactKeys(re.Client))
```

في `route`, `classifyError`, `classifyRateLimit`, و`makeFailure`. لا تُخزّن المفاتيح في `ProviderError` ولا تطبعها.

### اختبار idempotence الصحيح

أضف إلى `retry_policy_test.go`:

```go
func TestSanitizeBodyIsIdempotent(t *testing.T) {
    keys := []string{"provider-secret-that-has-no-known-prefix-123456"}
    input := `{"error":{"message":"key=provider-secret-that-has-no-known-prefix-123456"}}`

    once := SanitizeBody(input, keys)
    twice := SanitizeBody(once, keys)
    if once != twice {
        t.Fatalf("SanitizeBody is not idempotent:\nonce=%q\ntwice=%q", once, twice)
    }
    if strings.Contains(once, keys[0]) {
        t.Fatalf("exact key leaked: %q", once)
    }
}
```

## 4. سقف الإنفاق التراكمي بعد رفع `MaxTurns` إلى 40

### نوع الميزانية

أضف إلى `internal/agent/budget.go`:

```go
var ErrSpendBudget = errors.New("run token spend budget exhausted")

type Spend struct {
    Prompt     int
    Completion int
    Retries    int
    Fallbacks  int
    Compaction int
    Unknown    int
}

func (s Spend) Total() int {
    return s.Prompt + s.Completion + s.Retries + s.Fallbacks + s.Compaction + s.Unknown
}

type SpendBudget struct {
    Limit int
    Used  Spend
}

func (b *SpendBudget) Add(s Spend) error {
    if b == nil || b.Limit <= 0 {
        return nil
    }
    if b.Used.Total()+s.Total() > b.Limit {
        return ErrSpendBudget
    }
    b.Used.Prompt += s.Prompt
    b.Used.Completion += s.Completion
    b.Used.Retries += s.Retries
    b.Used.Fallbacks += s.Fallbacks
    b.Used.Compaction += s.Compaction
    b.Used.Unknown += s.Unknown
    return nil
}
```

أضف إلى `Loop`:

```go
SpendBudget *SpendBudget
```

واقرأ `NABD_MAX_TOKENS_PER_RUN` مرة واحدة عند إنشاء الحلقة. عند وصول chunk نهائي مع usage، سجّل `PromptTokens` و`CompletionTokens`. عند غياب usage، لا تعتبر الاستهلاك صفرًا؛ استخدم تقديرًا محافظًا أو زد `Unknown` بمقدار `maxOutputTokens()`.

أوقف الجولة قبل طلب جديد:

```go
if l.SpendBudget != nil && l.SpendBudget.Used.Total() >= l.SpendBudget.Limit {
    _ = l.emit(Event{Type: RunError, Err: ErrSpendBudget.Error()})
    return ErrSpendBudget
}
```

سجّل retry وfallback وcompaction صراحةً حتى لا يكون السقف قابلًا للتجاوز بسبب المسارات غير الناجحة.

## 5. مهلة مستقلة لدور المزود

لا تعتمد على مهلة التطبيق العامة وحدها. أضف إعدادًا مثل `NABD_PROVIDER_TURN_TIMEOUT`، ثم استخدمه حول استدعاء الجولة:

```go
func providerTurnContext(parent context.Context) (context.Context, context.CancelFunc) {
    timeout := 2 * time.Minute
    if raw := os.Getenv("NABD_PROVIDER_TURN_TIMEOUT"); raw != "" {
        if d, err := time.ParseDuration(raw); err == nil && d > 0 {
            timeout = d
        }
    }
    return context.WithTimeout(parent, timeout)
}
```

وفي موضع `Provider.Stream`:

```go
turnCtx, cancel := providerTurnContext(ctx)
ch, err := l.Provider.Stream(turnCtx, req)
if err != nil {
    cancel()
    return err
}
// defer cancel() after the stream has been drained.
```

يجب اختبار أن provider عالقًا يتوقف عند انتهاء مهلة الدور، وأن `Run` يعيد `context.DeadlineExceeded` أو خطأً مغلفًا قابلًا لـ`errors.Is`.

## 6. Circuit breaker لمسارات 401 و403

الهدف ليس حذف fallback، بل منع إعادة ضرب مسار غير صالح خلال الجلسة الحالية.

```go
type routeBreaker struct {
    mu      sync.Mutex
    blocked map[string]time.Time
    cooldown time.Duration
}

func (b *routeBreaker) key(r Route) string { return r.Provider + "\x00" + r.Model }

func (b *routeBreaker) Allow(r Route, now time.Time) bool {
    b.mu.Lock()
    defer b.mu.Unlock()
    until := b.blocked[b.key(r)]
    return until.IsZero() || !now.Before(until)
}

func (b *routeBreaker) Trip(r Route, now time.Time) {
    b.mu.Lock()
    defer b.mu.Unlock()
    b.blocked[b.key(r)] = now.Add(b.cooldown)
}
```

استدعِ `Allow` قبل المحاولة، و`Trip` فقط عند 401 أو 403. لا تُسجّل أي مفتاح في مفتاح breaker. أضف اختبارًا يثبت أن المسار المحظور يُتجاوز، وأنه يعود بعد انتهاء cooldown.

## 7. provenance لمنع fabricated read continuation

لا تعتمد على تحليل نص العرض للبحث عن عبارات مثل `continued file content`. يجب أن يحمل الحدث مصدره بنيويًا.

أضف نوعًا صريحًا:

```go
type Provenance uint8

const (
    ProvenanceUnknown Provenance = iota
    ProvenanceAssistantText
    ProvenanceToolEndReadFile
)
```

وفي حدث أداة القراءة:

```go
Event{
    Type:       ToolEnd,
    ToolName:   "read_file",
    Provenance: ProvenanceToolEndReadFile,
    Text:       output,
}
```

وفي العرض:

```go
func renderReadOutput(e Event) string {
    if e.Provenance != ProvenanceToolEndReadFile {
        return ""
    }
    return e.Text
}
```

أضف اختبارًا يرسل `Assistant TextDelta` يحتوي عبارة تشبه محتوى ملف، ويتأكد من عدم عرضه كملف. وأضف اختبارًا مقابلًا لـ`ToolEnd(read_file)` مع provenance صحيح.

## 8. بوابة CI

أضف workflow قصيرًا قبل الدمج:

```yaml
name: Go checks

on:
  pull_request:
  push:
    branches: [master]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - run: test -z "$(gofmt -l .)"
      - run: go vet ./...
      - run: go test ./...
      - run: go test -race ./...
```

إذا كان `go test -race ./...` مكلفًا جدًا، يمكن وضعه في job منفصل، لكن لا ينبغي حذفه من الحماية الليلية.

## 9. ترتيب التنفيذ المقترح

ينفذ الفريق التغييرات بهذا الترتيب:

1. دمج إصلاح P0 الحالي وتشغيل الاختبارات.
2. إضافة `NewJSONLExclusive` وربطها بكل مسارات إنشاء الجلسة الجديدة.
3. إضافة exact-key provider redaction واختبار idempotence.
4. إضافة spend budget مع سياسة usage المفقود.
5. إضافة timeout مستقل للدور.
6. إضافة circuit breaker للمسارات 401/403.
7. إضافة provenance للأحداث والعرض.
8. تفعيل CI وتشغيل `go test -race ./...`.
9. التحقق من الفروع المدموجة ثم حذفها فقط بعد موافقة تشغيلية واضحة.

## 10. أوامر التحقق بعد تثبيت Go

```bash
gofmt -w internal/agent/compact.go \
  internal/agent/rewind.go \
  internal/agent/persistence_failure_test.go

go test ./internal/agent ./internal/provider ./internal/store ./cmd/ag
go test -race ./internal/agent ./internal/store
go vet ./...
git diff --check
git status --short
git diff --stat
```

النتيجة المطلوبة هي نجاح جميع الاختبارات، وعدم وجود فرق تنسيق، وعدم وجود حدث `Compact` أو `Rewind` في الذاكرة عند فشل sink.

## مراجع

[1]: https://github.com/amiraq1/Nabd "Nabd source repository"
[2]: https://pkg.go.dev/os "Go os package documentation"
[3]: https://pkg.go.dev/context "Go context package documentation"
