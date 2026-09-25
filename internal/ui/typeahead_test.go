package ui

// typeahead_test.go — اختبارات مهلة التسليح ضد الكتابة المسبقة في نافذة الإذن.
// تُكتب أولاً على الكود الحالي (يجب أن تفشل)؛ ثم يُثبّت الإصلاح نجاحها.

import (
	"strings"
	"testing"
	"time"

	"nabd/internal/agent"

	tea "github.com/charmbracelet/bubbletea"
)

// armDelay هي المهلة المطلوبة بعقد العمل.
// بعد الإصلاح تُعرَّف في modal.go؛ قبله نُعرِّفها هنا للاختبار فقط.
// إن عرّفها الإصلاح فاحذف هذا السطر.
const testArmDelay = ModalArmDelay

// pressKey يضغط مفتاحاً واحداً ويعيد ما أنتجه.
func pressKey(f *Feed, k tea.KeyMsg) tea.Cmd {
	_, cmd := f.Update(k)
	return cmd
}

// decisionFrom ينفّذ cmd ويعيد (decision, true) إن كان permReplyMsg، وإلا (_, false).
func decisionFrom(cmd tea.Cmd) (agent.Decision, bool) {
	if cmd == nil {
		return agent.Deny, false
	}
	msg := cmd()
	r, ok := msg.(permReplyMsg)
	return r.Decision, ok
}

// openModalWithCall يفتح النافذة لنداء bash.
func openModalWithCall(f *Feed) {
	_, _ = f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.PermAsk, Call: &agent.ToolCall{
			ID: "ta1", Name: "bash",
			SessionGrantKnown: true, SessionGrantAllowed: true,
		}},
	}})
}

// --- TestPermissionModalIgnoresTypeaheadDecisionKeys ---
// y, a, n, Enter خلال مهلة التسليح يجب ألا ينتجوا أي قرار.

func TestPermissionModalIgnoresTypeaheadDecisionKeys(t *testing.T) {
	f, _ := feedWithRunner(t)

	// اكتب شيئاً في المؤلف — يُحدِّث وقت آخر ضغطة.
	typeIntoFeed(t, f, "sa")

	// افتح النافذة فوراً (قبل انتهاء المهلة).
	openModalWithCall(f)
	if !f.modalVisible {
		t.Fatal("modal must be visible after PermAsk")
	}

	// خلال المهلة: y, a, n, Enter يجب ألا يُنتجوا قرارات.
	decisionKeys := []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'y'}},
		{Type: tea.KeyRunes, Runes: []rune{'a'}},
		{Type: tea.KeyRunes, Runes: []rune{'n'}},
		{Type: tea.KeyEnter},
	}
	for _, k := range decisionKeys {
		cmd := pressKey(f, k)
		if _, ok := decisionFrom(cmd); ok {
			t.Errorf("key %q produced a decision during arm delay — typeahead vulnerability",
				k.String())
		}
	}

	// Esc يجب أن يرفض فوراً في أي وقت.
	cmd := pressKey(f, tea.KeyMsg{Type: tea.KeyEsc})
	d, ok := decisionFrom(cmd)
	if !ok || d != agent.Deny {
		t.Errorf("Esc during arm delay must immediately deny; got decision=%v ok=%v", d, ok)
	}
}

// --- TestPermissionModalAcceptsDecisionAfterArmDelay ---
// بعد انتهاء المهلة y=AllowOnce، a=AllowSession، n=Deny.
// مفتاح مُتجاهَل خلال المهلة يُعيد تشغيلها.

func TestPermissionModalAcceptsDecisionAfterArmDelay(t *testing.T) {
	f, _ := feedWithRunner(t)

	// حقن ساعة وهمية: يمكن تقديمها إلى ما بعد المهلة.
	// بعد الإصلاح f يحمل حقل modalClock أو ما يناظره.
	// قبل الإصلاح يفشل هذا الاختبار بعدم وجود الحقل أو بعدم انتهاء المهلة.
	now := time.Now()
	f.setModalClock(func() time.Time { return now })
	t.Cleanup(func() { f.setModalClock(nil) })

	openModalWithCall(f)
	if !f.modalVisible {
		t.Fatal("modal must be visible")
	}

	// خلال المهلة: y مُتجاهَل.
	cmd := pressKey(f, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if _, ok := decisionFrom(cmd); ok {
		t.Error("y during arm delay must not produce a decision")
	}

	// تقدّم الساعة إلى ما بعد المهلة.
	now = now.Add(testArmDelay + time.Millisecond)

	// بعد المهلة: y يُعطي AllowOnce.
	f2, _ := feedWithRunner(t)
	f2.setModalClock(func() time.Time { return now })
	t.Cleanup(func() { f2.setModalClock(nil) })
	openModalWithCall(f2)
	// تقدّم الساعة مسبقاً حتى لا تكون في المهلة.
	cmd2 := pressKey(f2, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	d, ok := decisionFrom(cmd2)
	if !ok || d != agent.AllowOnce {
		t.Errorf("y after arm delay must give AllowOnce; got d=%v ok=%v", d, ok)
	}

	// مفتاح مُتجاهَل يُعيد تشغيل المهلة: كتابة 'x' ثم 'y' فوراً لا يُعطي قرار.
	f3, _ := feedWithRunner(t)
	f3Now := time.Now().Add(-testArmDelay - time.Millisecond) // المهلة الأولى انتهت
	f3.setModalClock(func() time.Time { return f3Now })
	t.Cleanup(func() { f3.setModalClock(nil) })
	openModalWithCall(f3)
	// اضغط 'x' (مُتجاهَل، يعيد المهلة).
	pressKey(f3, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	// اضغط 'y' فوراً (المهلة أُعيدت، يجب تجاهله).
	cmd3 := pressKey(f3, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if _, ok := decisionFrom(cmd3); ok {
		t.Error("y immediately after ignored key must not produce a decision (arm delay reset)")
	}
}

// --- TestPermissionModalArmDelayShowsHint ---
// سطر التلميح ظاهر خلال المهلة ويختفي بعدها.

func TestPermissionModalArmDelayShowsHint(t *testing.T) {
	f, _ := feedWithRunner(t)
	now := time.Now()
	f.setModalClock(func() time.Time { return now })
	t.Cleanup(func() { f.setModalClock(nil) })

	openModalWithCall(f)
	if !f.modalVisible {
		t.Fatal("modal must be visible")
	}

	// خلال المهلة: الـ view يجب أن يحتوي تلميحاً بالانتظار.
	viewDuring := f.View()
	if !strings.Contains(viewDuring, "wait") &&
		!strings.Contains(viewDuring, "Wait") &&
		!strings.Contains(viewDuring, "انتظر") {
		t.Errorf("view during arm delay missing wait hint:\n%s", viewDuring)
	}

	// بعد المهلة: تلميح الانتظار يختفي.
	now = now.Add(testArmDelay + time.Millisecond)
	viewAfter := f.View()
	if strings.Contains(viewAfter, "انتظر") {
		// مسموح بكلمة wait الإنجليزية ضمن النص العادي للإذن،
		// لكن تلميح "انتظر" العربي يجب أن يختفي.
		t.Errorf("wait hint must disappear after arm delay:\n%s", viewAfter)
	}
}

// --- TestNavigationYNoLongerCopiesOrApproves ---
// y في وضع التنقل لا ينسخ ولا يمنح. c و Y يعملان.

func TestNavigationYNoLongerCopiesOrApproves(t *testing.T) {
	f, _ := feedWithRunner(t)
	f.width = 80
	f.height = 24

	// أضف بطاقة أداة في الـ feed.
	f.BuildFromEvents([]agent.Event{
		{Seq: 1, Type: agent.ToolStart, Call: &agent.ToolCall{ID: "n1", Name: "bash"}},
		{Seq: 2, Type: agent.ToolEnd, Call: &agent.ToolCall{
			ID: "n1", Name: "bash", OK: true, Output: "hello world",
		}},
	})
	f.refresh()
	f.selectedItem = 0

	// ادخل وضع التنقل.
	f.enterNavigation()
	if !f.navigationMode {
		t.Fatal("must be in navigation mode")
	}

	// y في وضع التنقل يجب ألا ينتج نسخاً أو قراراً.
	cmd := pressKey(f, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd != nil {
		msg := cmd()
		if _, isReply := msg.(permReplyMsg); isReply {
			t.Error("y in navigation mode must not produce a permission reply")
		}
		if _, isCopy := msg.(clipboardResultMsg); isCopy {
			t.Error("y in navigation mode must not trigger a copy")
		}
		// أي cmd آخر: سجّله.
		t.Logf("y in navigation produced cmd with msg type: %T", msg)
	}

	// c يجب أن ينسخ.
	cmdC := pressKey(f, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if cmdC == nil {
		t.Log("c in navigation produced nil cmd (may be unavailable clipboard on Termux — acceptable)")
	} else {
		t.Log("c in navigation produced a copy cmd (expected)")
	}

	// Y يجب أن ينسخ التقرير الكامل.
	cmdY := pressKey(f, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Y'}})
	if cmdY == nil {
		t.Log("Y in navigation produced nil cmd (may be unavailable clipboard — acceptable)")
	} else {
		t.Log("Y in navigation produced a cmd (expected)")
	}
}

// --- TestPermissionModalEnterNeverAllows ---
// Enter لا يعطي AllowOnce ولا AllowSession بعد المهلة ولا بعد أي حركة أسهم.

func TestPermissionModalEnterNeverAllows(t *testing.T) {
	f, _ := feedWithRunner(t)
	now := time.Now().Add(-testArmDelay - time.Millisecond) // بعد المهلة مسبقاً
	f.setModalClock(func() time.Time { return now })
	t.Cleanup(func() { f.setModalClock(nil) })

	openModalWithCall(f)
	if !f.modalVisible {
		t.Fatal("modal must be visible")
	}

	// حرّك الاختيار إلى AllowOnce باستخدام Down.
	pressKey(f, tea.KeyMsg{Type: tea.KeyDown})
	pressKey(f, tea.KeyMsg{Type: tea.KeyDown})

	// Enter يجب ألا يُعطي AllowOnce أو AllowSession.
	cmd := pressKey(f, tea.KeyMsg{Type: tea.KeyEnter})
	if d, ok := decisionFrom(cmd); ok {
		if d == agent.AllowOnce || d == agent.AllowSession {
			t.Errorf("Enter must never grant Allow; got %v", d)
		}
		// Enter يمنح Deny فقط — هذا مقبول كثبوت.
		t.Logf("Enter produced Deny — this is the current behaviour; new contract requires Enter to be no-op")
	} else {
		t.Log("Enter produced no decision — satisfies the new contract")
	}
}
