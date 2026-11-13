# نموذج التهديدات — NabdCoder

هذا المستند يحدد ما يحاول NabdCoder حمايته، ومن هم الخصوم، وما الذي يضمنه النظام فعلياً، وما لا يضمنه. يعتمد على الكود والاختبارات في هذا المستودع، وليس على نوايا تصميمية غير منفذة.

## 1. النطاق

يشمل:

- وكيل `cmd/agent` وأدواته في `internal/tools`.
- خادم `cmd/mcp-server` (للقراءة فقط).
- التخزين المحلي: `.nabd/shadow` و`.nabd/audit.log`.
- عميل LLM في `internal/llm` وبيئة التشغيل.

لا يشمل:

- أمان نظام Termux/Android نفسه، أو صلاحيات التطبيق.
- أمان خادم LLM البعيد أو مزوّد الشبكة.
- أمان مستودع Git البعيد أو حسابات المطوّرين.
- عزل على مستوى kernel/container؛ لا يوجد sandbox.

## 2. الأصول

- **ملفات المشروع**: سرية وسلامة (كود، إعدادات، بيانات).
- **الأسرار**: `NABD_API_KEY`, متغيرات البيئة، ملفات المفاتيح، `.env`, `.git/config`.
- **سجل التدقيق ونسخ shadow**: سلامة السجل وقابلية الاستعادة.
- **الطرفية**: منع تزوير الواجهة أو مطالبات الموافقة.
- **الجهاز**: الذاكرة والقرص (Termux محدود الموارد).

## 3. حدود الثقة

1. **المستخدم ↔ TUI**: المستخدم موثوق؛ الطرفية قد تكون غير موثوقة إذا كانت مشتركة.
2. **TUI ↔ endpoint LLM**: الـendpoint غير موثوق؛ كل رسالة تُرسل قد تحمل محتوى workspace.
3. **النموذج ↔ الأدوات**: النموذج غير موثوق؛ قد يكون مخترقاً أو متأثراً بـprompt injection.
4. **الأدوات ↔ workspace**: المسارات والملفات غير موثوقة؛ قد تحتوي symlinks أو محتوى مضللاً.
5. **go_verify ↔ كود المشروع**: كود الاختبار غير موثوق؛ ينفّذ بصلاحيات المستخدم.
6. **mcp_call ↔ عمليات خارجية**: binary خارجي غير موثوق؛ يعمل بصلاحيات المستخدم.
7. **mcp-server ↔ MCP client**: العميل غير موثوق؛ الخادم يقيّد نفسه للقراءة فقط.

## 4. الخصوم والقدرات

- **نموذج مخترق أو prompt injection**: يستطيع طلب أي أداة، وصياغة وسائط، وإخفاء تعليمات داخل الملفات أو مخرجات الأدوات.
- **ملفات مشروع خبيثة**: ANSI/control sequences، مسارات ملتوية، symlinks، أسماء أسرار، مخرجات أدوات مضللة.
- **مستخدم محلي آخر**: يقرأ/يحذف `.nabd`، يعرف مسارات الملفات، ولا يملك elevated privileges.
- **endpoint بعيد**: يرى الرسائل كاملة، وقد يحتفظ بها.
- **عملية MCP خارجية**: قد تنفّذ أي شيء عند تشغيلها، بما في ذلك الوصول للشبكة.
- **كود اختبار خبيث**: يُنفَّذ عبر `go_verify` ويمكنه قراءة الملفات وفتح sockets.

## 5. ما يضمنه النظام

- **احتواء المسارات**: `os.Root` على جذر الـworkspace لكل عمليات الملفات، مع تحقق إضافي من `..` والمسارات المطلقة وvolume-relative وNUL. `edit_file` و`undo` يرفضان symlink. (اختبارات: `TestJailRejectsEscapes`, `TestJailRejectsSymlinkEscape`, `TestEditThroughSymlinkedParentRejected`.)
- **سياسة المسارات الحساسة**: `.nabd/`, `.git/`, `.env*` (عدا القوالب), `id_*` (عدا `.pub`), وملفات المفاتيح مرفوضة لأدوات الملفات، ومخفية من `list_dir`. (اختبارات: `TestSensitivePathsDenied`, `TestShadowDeniedToFileTools`.)
- **بوابة الموافقة**: preview يعرض المسار وdiff وSHA-256، وإعادة فحص hash الأساس قبل التنفيذ. `RunApproved` يتطلب token أحادي الاستخدام مربوطاً بالأداة و`args_sha256`، صالح 5 دقائق. `Run` بدون approver يفشل مغلقاً. (اختبارات: `TestApprovalPreviewEdit`, `TestApprovalGuardDetectsFileChange`, `TestRunFailsClosedWithoutApprover`, `TestApprovalTokenRequiredAndSingleUse`, `TestApprovalTokenExpires`.)
- **تدقيق إلزامي للكتابة**: `requested`/`approved` fail-closed قبل التنفيذ؛ تعذّر الكتابة يمنع التعديل/التنفيذ. القراءة تعمل بدون تدقيق. (اختبارات: `TestAuditLogFailClosedForWrites`, `TestReadOnlyToolsSurviveAuditFailure`, `TestAuditLogApprovalFlow`.)
- **تعديل آمن**: كتابة ذرّية temp+fsync+rename مع حفظ mode، رفض no-op وsymlink والملفات >2MiB، CRLF محفوظ، ونسخة shadow قبل التعديل. (اختبارات: `TestEditAtomicPreservesModeAndLeavesNoTemp`, `TestEditPreservesCRLFInGo`, `TestEditRejectsSymlink`, `TestEditRejectsLargeFile`.)
- **استعادة محكومة**: `undo` يستعيد فقط إذا طابق محتوى الملف `sha256_after`، ويرفض symlink، ويعلّم النسخة `undone`. (اختبارات: `TestUndoRestoresFile`, `TestUndoRefusesAfterDrift`, `TestUndoNoArgRestoresLatest`, `TestUndoIsAudited`.)
- **go_verify محكوم**: بيئة allowlist بلا مفاتيح أو proxy، `GOFLAGS=-mod=readonly`, `GOPROXY=off`, `GOTOOLCHAIN=local`، أنماط حزم workspace-relative، مهلة 90s تقتل شجرة العمليات، ونتيجة منظمة. (اختبارات: `TestGoVerifyEnvAllowlist`, `TestValidatePkgPattern`, `TestGoVerifyTimeout`, `TestProcessGroupKill` على Unix.)
- **LLM مضبوط**: إعداد من البيئة فقط، مهلة كلية، إلغاء عند Ctrl+C، retry محدود على 429/5xx/الشبكة فقط، وميزانية سياق تقديرية مع trim واعٍ للأزواج. (اختبارات: `TestAgentCancelsRequestOnCtrlC`, `TestChatRetriesOn429And5xx`, `TestChatDoesNotRetry400`, `TestTrimHistoryKeepsToolPairs`.)
- **واجهة مقاومة للتزوير**: إزالة ANSI/C0/C1 قبل العرض، clamp للطرفية، وخروج عند non-TTY. (اختبارات: `TestSanitizeForDisplay`, `TestApprovalPreviewSanitizesANSIText`, `TestTinyWindowDoesNotPanic`, `TestEnsureInteractiveRejectsNonTTY`.)
- **mcp-server للقراءة فقط**: يرفض الأدوات غير read-only لغياب approver، و`mcp_call` معطّل. (اختبار: `TestServerIsReadOnlyAndDisablesMCPCall`.)

## 6. ما لا يضمنه النظام

- **لا sandbox**: `go_verify` و`mcp_call` ينفّذان كوداً/عمليات بصلاحيات المستخدم. الموافقة خطوة بشرية، وليست عزلاً.
- **شبكة كود الاختبار**: `GOPROXY=off` يمنع تنزيل وحدات Go فقط؛ كود الاختبار يستطيع فتح sockets مباشرة.
- **`mcp_call`**: يشغّل binary موجوداً على النظام؛ لا allowlist ولا احتواء، و`cmd` لا يستقبل وسائط. الـpreview يعرض الأمر لكنه لا يمنع الضرر.
- **تسريب المحتوى إلى LLM**: `read_file` ومخرجات الأدوات تُرسل إلى endpoint الـLLM. إذا كان بعيداً، فمحتوى ملفات المشروع يخرج من الجهاز.
- **سياسة الأسرار بالأسماء فقط**: سر داخل `config.yaml` أو ملف غير مطابق للأنماط يمكن قراءته.
- **سلامة سجل التدقيق**: append-only بالبناء، لكنه ليس tamper-evident؛ مستخدم محلي يستطيع حذف/تفريغ `.nabd/audit.log`. لا hash chain ولا تدوير بعد.
- **TOCTOU داخل الجذر**: بين preview والتنفيذ قد يتغيّر محتوى الملف داخل الـworkspace. hash guard + التطابق الفريد يخففان، لكن لا يوجد قفل ملف.
- **hard links**: الكتابة الذرّية عبر rename تستبدل الاسم؛ الأسماء الأخرى المرتبطة تحتفظ بالمحتوى القديم.
- **Windows**: `taskkill /T` أفضل جهد، والصلاحيات approximated.
- **Termux DNS/TLS**: غير مؤكدين لثنائي `CGO_ENABLED=0`؛ راجع فحص الجهاز في README.
- **لا حدود معدل/حصص**: لا rate limiting لاستدعاءات الأدوات أو الـLLM.
- **لا LICENSE**: لم يُضف ترخيص قانوني بعد.

## 7. سيناريوهات واختبارات

- **هروب مسار/abs/UNC/NUL** → `TestJailRejectsEscapes`.
- **symlink خارج الجذر** → `TestJailRejectsSymlinkEscape` و`TestEditThroughSymlinkedParentRejected`.
- **قراءة أسرار بالاسم** → `TestSensitivePathsDenied`.
- **تجاوز البوابة بلا approver** → `TestRunFailsClosedWithoutApprover`.
- **إعادة استخدام token أو استخدامه لوسائط أخرى** → `TestApprovalTokenRequiredAndSingleUse`.
- **انتهاء token** → `TestApprovalTokenExpires`.
- **تعذّر التدقيق** → `TestAuditLogFailClosedForWrites`.
- **تزوير ANSI** → `TestSanitizeForDisplay` و`TestApprovalPreviewSanitizesANSIText`.
- **طرفية صغيرة/non-TTY** → `TestTinyWindowDoesNotPanic` و`TestEnsureInteractiveRejectsNonTTY`.
- **تسريب بيئة الاختبار** → `TestGoVerifyEnvAllowlist`.
- **أنماط حزم خارجية** → `TestValidatePkgPattern`.
- **تعليق شجرة عمليات** → `TestProcessGroupKill` (Unix) و`TestGoVerifyTimeout` (الكل).
- **حلقة أدوات لا نهائية/متكررة** → `TestMaxIterationsSynthesizesToolErrors` و`TestRepeatedCallsBlocked`.
- **إلغاء طلب LLM** → `TestAgentCancelsRequestOnCtrlC`.
- **retry غير صحيح** → `TestChatRetriesOn429And5xx` و`TestChatDoesNotRetry400`.
- **كسر أزواج tool calls** → `TestTrimHistoryKeepsToolPairs`.
- **تعديل غير ذرّي/فقدان mode** → `TestEditAtomicPreservesModeAndLeavesNoTemp`.
- **undo بعد تغيير الملف** → `TestUndoRefusesAfterDrift`.
- **خادم MCP يعدّل ملفاً** → `TestServerIsReadOnlyAndDisablesMCPCall`.

## 8. المخاطر المتبقية وأولوياتها

- **P1**: عزل/allowlist لـ`mcp_call`، وsandbox أو تقييد شبكي/ملفي لـ`go_verify` إذا كان النموذج غير موثوق.
- **P2**: سلسلة hash (`prev_hash`/`entry_hash`) وتدوير سجل التدقيق.
- **P2**: كشف/تنقيح الأسرار بالمحتوى لا بالاسم.
- **P2**: `os.Root` + قفل/معاملة تغلق TOCTOU بين preview والتنفيذ.
- **P3**: Job Objects على Windows بدل `taskkill`، وضبط `max_tokens`/`temperature`، وstreaming، وترخيص.

## 9. التحقق على Termux

راجع قسم «التحقق على Termux» في [README](../README.md): كتلة probe واحدة مع المخرجات المتوقعة لـDNS/TLS/HTTPS. النتيجة تُحدد ما إذا كان `CGO_ENABLED=0` يحتاج ضبط `SSL_CERT_FILE` أو مسار DNS محلي.
