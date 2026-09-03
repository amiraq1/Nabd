# nabd — نبض

وكيل برمجة طرفي، يُكتب ويُشغَّل من هاتف. بلا حاويات، بلا قاعدة بيانات، بلا CGO.
سبعة آلاف سطر Go تقريبًا، ملف تنفيذي واحد، وسجلّ جلسة واحد يمكن قراءته بـ `cat`.

**A terminal coding agent built on a phone.** Append-only event journal,
default-deny permissions, git-backed undo, and a containment story that is
written down rather than assumed.

---

## لماذا

معظم وكلاء البرمجة يخفون ما فعلوه خلف واجهة جميلة. `nabd` يقلب الترتيب:
الجلسة سجلّ أحداث قبل أن تكون واجهة، والواجهة مجرّد قارئ لذلك السجلّ.
كل ما تراه أثناء التشغيل يمكن إعادة عرضه بعده حرفًا بحرف، لأن الحيّ والمُعاد
يمرّان على نفس دالة العرض.

هذا ليس تفصيلًا جماليًا. هو ما يجعل التراجع ممكنًا، والتدقيق ممكنًا،
واستئناف الجلسة أمس ممكنًا اليوم.

## التشغيل

```sh
go build -o ag ./cmd/ag

export ANTHROPIC_API_KEY=...      # أو
export NVIDIA_API_KEY=nvapi-...   # أي مزوّد يتكلّم لهجة OpenAI

./ag                              # محادثة جديدة في المجلد الحالي
./ag --continue                   # استئناف آخر جلسة
./ag --replay <file.jsonl>        # إعادة عرض جلسة، --speed 0 للفوري
```

الأفضل ألّا يمرّ المفتاح بالبيئة أصلًا. ضعه في `~/.ag/config` بصلاحيات `600`:

```sh
mkdir -p ~/.ag && touch ~/.ag/config && chmod 600 ~/.ag/config
cat >> ~/.ag/config <<'EOF'
ANTHROPIC_API_KEY=sk-ant-...
NABD_MODEL=claude-sonnet-4-5
EOF
```

الملف `KEY=VALUE` سطرًا سطرًا، يتسامح مع `export` والاقتباس و`#` للتعليق.
ما في الملف يغلب ما في البيئة. ملفٌ مقروء للغير يُرفض عند التشغيل ولا يُقرأ.
`NABD_CONFIG` يغيّر موقعه.

متغيّرات اختيارية (في الملف أو البيئة): `NABD_PROVIDER` لفرض مزوّد،
`NABD_MODEL` لاسم الموديل، `NABD_BASE_URL` لخادم متوافق، `NABD_CTX` لحجم
نافذة السياق.

داخل المحادثة: `/undo [n]` و `/edits` و `/rewind [n]` و `/ctx` و `/compact` و `/help`.
عند سؤال الإذن: `y` مرّة واحدة، `a` لبقية الجلسة، `n` رفض. `ctrl+c` يوقف الدور،
`ctrl+d` يخرج.

## المعمار في ستّ فقرات

**الحدث هو العقد.** كل ما يجري — رسالة، جزء نصّ، نداء أداة، سؤال إذن، خطأ،
مقاطعة — حدثٌ بحقول مسطّحة يُلحق بملف `session.jsonl` ولا يُعدَّل بعدها.
الواجهة تتغيّر كل يوم؛ هذا الملف لا.

**الملف شجرة لا قائمة.** كل حدث يحمل `parent`، فالسجلّ المُلحَق فقط يصف شجرة.
`/rewind` لا يحذف شيئًا: يُلحق حدثًا يشير أبوه إلى ما قبل الدور المقصوص،
فيصبح الفرع بعده غير قابل للوصول من `Live()` وباقيًا على القرص إلى الأبد.
القصّ إضافة، لا حذف.

**الرفض هو القيمة الصفرية.** `Decision(0) == Deny`. إذن مفقود، أو حقل غائب من
JSON، أو نصّ غير معروف — كلّها تُقرأ رفضًا، ويحرس ذلك اختبار مستقلّ. هذا القرار
جاء من قراءة وكيل آخر ينهار إلى السماح عند الغموض.

**الاحتواء بوّابة واحدة.** `Root.Resolve` هي الدالة الوحيدة المسموح لها بقبول
مسار: تحلّ أعمق سلف موجود بـ `EvalSymlinks`، تُلحق الذيل الناقص، ثم تقارن بـ
`filepath.Rel` — بهذا الترتيب، لأن التنظيف قبل الحلّ هو الثغرة الكلاسيكية،
والمجلد الرمزي بذيل غير موجود هو الثغرة الخبيثة. اثنتا عشرة صورة هروب مُختبَرة.

**التراجع يمرّ على git.** قبل كل كتابة تُلتقط الحالة السابقة بـ `git hash-object`
(أو SHA-256 في `.ag/shadow` خارج المستودعات). الكتابة ذرّية عبر ملف مؤقّت ثم
`rename`، ثم يُعاد القراءة ويُقارن الهاش: كتابة لا يمكن إثباتها لم تحدث.
و`/undo` يرفض إن تغيّر الملف بعد أن كتبه الوكيل — أن يرفض التراجع أهون من أن
يدهس عملك.

**الضغط قيدٌ لا مقصّ.** حين يقارب السياق ثلاثة أرباع النافذة تُلحق إدخالة
`compact` تحمل `first_kept` وملخّصًا؛ لا يُعاد كتابة شيء. الحدّ يقع على رسالة
مستخدم حصرًا، لأن القطع داخل دور يترك `tool_result` بلا `tool_use` فيُرفض
الطلب التالي كليًا. وقبل الملخّص الغالي يمرّ مقصّ رخيص يُفرغ مخرجات الأدوات
القديمة ويُبقي الأخطاء — الخطأ قصير ويمنع التكرار.

## الحدود — اقرأها قبل أن تثق

هذه ليست فقرة تواضع. هي ما تحتاج معرفته قبل أن تعطي الأداة مجلدًا يهمّك.

**`bash` تخرج من الصندوق.** أداة `bash` تعمل في مجلد المشروع لكنها لا تمرّ
بـ `Resolve` ولا يمكنها أن تمرّ به. `cd ..` تعمل. `rm -rf ~/x` تعمل. `snap` لا
ترى شيئًا من ذلك، فـ `/undo` **لا يغطّي أي أمر صدفة**. الحماية الوحيدة هناك
عينك عند سؤال الإذن، ولهذا `bash` مصنّفة `Executing` ولا تقبل تصريح جلسة
مهما ضغطت `a`.

**تنظيف البيئة يحمي من الصدفة لا من الخصم.** `scrubEnv` يُزيل المفاتيح من
بيئة الأمر، لكن `cat ~/.bashrc` سطر واحد إن كان المفتاح مُصدَّرًا هناك. لهذا
يُقرأ المفتاح من `~/.ag/config` أوّلًا: ملف خارج الجذر لا يكتبه البرنامج أبدًا
ويرفضه إن كان مفتوحًا للغير. البيئة ما زالت تعمل، لكنها الخيار الأضعف.

**نافذة TOCTOU.** بين `Resolve` وفتح الملف فعليًا لحظة يمكن نظريًا استبدال
مكوّن مسار برابط رمزي. الحلّ الصحيح `openat2` بـ `RESOLVE_BENEATH`، وهو
مؤجّل عمدًا: البيئة المستهدفة هاتف بمستخدم واحد.

**عدّ الرموز تخمين.** لا مُرمِّز داخل البرنامج؛ التقدير قاعدة تُميّز الأحرف
غير اللاتينية وتميل للمبالغة. تُعاير من `input_tokens` الحقيقية إن وصلت من
المزوّد. توقّع خطأً بحدود ١٠–٢٠٪ حتى تُعايَر.

**الملخّص نداء نموذج.** إن فشل، يسقط إلى ملخّص ميكانيكي يذكر طلبات المستخدم
حرفيًا والملفات التي مُسّت. لن تفقد الخيط، لكن ستفقد التفاصيل.

**عمليةٌ واحدة في المجلد الواحد.** سجلّ التعديلات يعيش في ذاكرة العملية.
نسختان من `ag` في نفس المستودع لن تريا تعديلات بعضهما، و`/undo` في إحداهما
لا تعرف شيئًا عن الأخرى.

**`killGroup` يقتل الخلفية المقصودة أيضًا.** `npm run dev &` لن ينجو بعد
انتهاء الأمر. هذا اختيار: خادمٌ يعيش بعد الأمر الذي أنشأه هو إذنٌ بلا انتهاء
صلاحية.

**Linux وmacOS فقط.** `Setpgid` و`sh -c` يمنعان ويندوز.

**ما هو مُختبَر وما ليس كذلك.** طبقات المسار والتخزين والأذونات والتراجع
والضغط والصدفة مغطّاة باختبارات وحدة. المسار الكامل — نموذج حيّ يطلب أداة،
والنتيجة تعود إليه في الدور التالي — مُجرَّب يدويًا لا آليًا. عامله على هذا
الأساس.

## البنية

`internal/agent` عقد الحدث والحلقة والشجرة والضغط والميزانية.
`internal/config` قراءة `~/.ag/config`؛ لا يكتب شيئًا أبدًا.
`internal/store` سجلّ JSONL بإلحاق ذرّي.
`internal/provider` واجهة المزوّد وتنفيذان: Anthropic، وأي خادم بلهجة OpenAI.
`internal/tools` الاحتواء وأدوات القراءة والكتابة والصدفة.
`internal/perm` بوّابة الأذونات.
`internal/snap` الظلّ والتراجع.
`internal/ui` العرض والمحادثة وإعادة العرض.
`cmd/ag` الربط، ولا شيء غيره.

قاعدة اتجاه واحدة: `agent` لا يستورد `tools` أبدًا.

## الأصل

كُتب من الصفر. سبقته قراءةٌ في معمار وكلاء آخرين لفهم القرارات لا لنسخها؛
لا سطر هنا منقول من مصدر مغلق، والأسماء والبنى والأخطاء كلها من هنا.
غير مرتبط بأي مزوّد نماذج ولا مدعوم منه.

## الرخصة

MIT License

Copyright (c) 2026 nabd contributors

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
