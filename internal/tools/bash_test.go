package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// TestBashRejectsNonStrictArgs pins the argument boundary itself, with repair
// disabled: that is the net behind RepairCall, and it is what any path that does
// not pass through repair still gets — a direct tool call, or the NBD-420
// measurement harness. A call carrying an undeclared key or a duplicate key is
// rejected before any subprocess starts, so the guarantee never rests on the
// repair layer having recognised the call.
func TestBashRejectsNonStrictArgs(t *testing.T) {
	r, _ := newReg(t)
	r.repairOff = true
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "unknown field",
			raw:  `{"cmd":"true","bogus":1}`,
			want: "unknown field",
		},
		{
			name: "duplicate key",
			raw:  `{"cmd":"true","cmd":"false"}`,
			want: "duplicate key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := r.RunDetailed(
				context.Background(),
				"bash",
				json.RawMessage(tt.raw),
			)
			if err == nil {
				t.Fatalf("RunDetailed accepted %s", tt.name)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %q does not contain %q", err, tt.want)
			}
		})
	}
}

func TestBashTimeoutKillsGrandchildren(t *testing.T) {
	r, _ := newReg(t)
	raw, _ := json.Marshal(map[string]any{
		"cmd":       "sleep 60 & echo $!; sleep 60",
		"timeout_s": 1,
	})
	o, err := r.RunDetailed(context.Background(), "bash", raw)
	if err != nil {
		t.Fatal(err)
	}
	if o.OK {
		t.Fatal("انتهى بنجاح رغم المهلة")
	}
	var pid int
	for _, ln := range strings.Split(o.Text, "\n") {
		if v, err := strconv.Atoi(strings.TrimSpace(ln)); err == nil && v > 1 {
			pid = v
			break
		}
	}
	if pid == 0 {
		t.Skip("لم أستخرج pid الحفيد")
	}
	time.Sleep(200 * time.Millisecond)
	if syscall.Kill(pid, 0) == nil {
		syscall.Kill(pid, syscall.SIGKILL)
		t.Fatal("الحفيد نجا من قتل المجموعة")
	}
}

func TestBashStdinIsDevNull(t *testing.T) {
	r, _ := newReg(t)
	raw, _ := json.Marshal(map[string]any{
		"cmd":       "cat",
		"timeout_s": 2,
	})

	start := time.Now()
	o, err := r.RunDetailed(context.Background(), "bash", raw)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatal(err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("استغرق وقتًا طويلًا (%v)، الـ stdin ليس مغلقًا", elapsed)
	}
	if !o.OK {
		t.Fatal("فشل الأمر cat")
	}
}

func TestBashCapturesExitCode(t *testing.T) {
	r, _ := newReg(t)
	raw, _ := json.Marshal(map[string]any{
		"cmd": "exit 3",
	})
	o, err := r.RunDetailed(context.Background(), "bash", raw)
	if err != nil {
		t.Fatal(err)
	}
	if o.OK {
		t.Fatal("يجب أن يفشل")
	}
	if o.Exit != 3 {
		t.Fatalf("رمز الخروج المتوقع 3، حصلت على %d", o.Exit)
	}
}

// TestBashCleanExitDoesNotKillProcessGroup guards the recycled-PGID boundary:
// a successful command must never signal its process group after Wait has
// reaped the leader. Only timeout and cancellation paths may call killGroup.
func TestBashCleanExitDoesNotKillProcessGroup(t *testing.T) {
	r, _ := newReg(t)
	realKillGroup := killGroupFn
	t.Cleanup(func() { killGroupFn = realKillGroup })

	var calls atomic.Int32
	killGroupFn = func(int) {
		calls.Add(1)
	}

	raw, _ := json.Marshal(map[string]any{
		"cmd": "printf clean",
	})
	o, err := r.RunDetailed(context.Background(), "bash", raw)
	if err != nil {
		t.Fatal(err)
	}
	if !o.OK {
		t.Fatalf("clean command failed: %s", o.Text)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("clean exit called killGroup %d time(s), want 0", got)
	}
}

func TestBashScrubsKeys(t *testing.T) {
	t.Setenv("FAKE_API_KEY", "secret_value")
	r, _ := newReg(t)
	raw, _ := json.Marshal(map[string]any{
		"cmd": "env",
	})
	o, err := r.RunDetailed(context.Background(), "bash", raw)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(o.Text, "secret_value") || strings.Contains(o.Text, "FAKE_API_KEY") {
		t.Fatal("المتغيرات السرية تسربت للصدفة")
	}
}

func TestBashRunsInRoot(t *testing.T) {
	r, dir := newReg(t)
	// Mac/Linux temp dirs can have symlinks like /var -> /private/var
	realDir, _ := filepath.EvalSymlinks(dir)

	raw, _ := json.Marshal(map[string]any{
		"cmd": "pwd",
	})
	o, err := r.RunDetailed(context.Background(), "bash", raw)
	if err != nil {
		t.Fatal(err)
	}
	if !o.OK {
		t.Fatal("فشل أمر pwd")
	}

	outDir := strings.TrimSpace(strings.Split(o.Text, "\n")[1])
	outRealDir, _ := filepath.EvalSymlinks(outDir)

	if outRealDir != realDir {
		t.Fatalf("مسار التنفيذ خاطئ: المتوقع %q، حصلت على %q", realDir, outRealDir)
	}
}

// TestHeadTailIOWriterContract verifies that headTail.Write honors the
// io.Writer contract: it always returns n == len(p) on success, regardless
// of how the bytes are distributed between head and tail buffers.
func TestHeadTailIOWriterContract(t *testing.T) {
	// headTail uses large constants: bashHead=8192, bashTail=24576.
	// To exercise all code paths, inputs must exceed these thresholds.

	// 1. Single write smaller than head capacity.
	h := &headTail{}
	input := []byte("hello world")
	n, err := h.Write(input)
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if n != len(input) {
		t.Fatalf("Write returned n=%d, want %d (io.Writer contract)", n, len(input))
	}
	if h.total != len(input) {
		t.Errorf("total=%d, want %d", h.total, len(input))
	}
	if string(h.head) != "hello world" {
		t.Errorf("head=%q, want \"hello world\"", h.head)
	}
	if len(h.tail) != 0 {
		t.Errorf("tail len=%d, want 0", len(h.tail))
	}

	// 2. Multiple small Write calls that stay within head.
	h = &headTail{}
	small := []byte("AAAA")
	for i := 0; i < 10; i++ {
		n, err := h.Write(small)
		if err != nil || n != len(small) {
			t.Fatalf("write %d: n=%d err=%v, want n=%d", i, n, err, len(small))
		}
	}
	if h.total != 40 {
		t.Errorf("total=%d, want 40", h.total)
	}

	// 3. Single write larger than head+tail (forces middle drop).
	h = &headTail{}
	big := make([]byte, bashHead+bashTail+1000)
	for i := range big {
		big[i] = byte('a' + (i % 26))
	}
	n, err = h.Write(big)
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if n != len(big) {
		t.Fatalf("Write returned n=%d, want %d", n, len(big))
	}
	if h.total != len(big) {
		t.Errorf("total=%d, want %d", h.total, len(big))
	}
	// head should be the first bashHead bytes.
	if string(h.head) != string(big[:bashHead]) {
		t.Errorf("head mismatch: len=%d want %d", len(h.head), bashHead)
	}
	// tail should be the last bashTail bytes.
	if string(h.tail) != string(big[len(big)-bashTail:]) {
		t.Errorf("tail mismatch: len=%d want %d", len(h.tail), bashTail)
	}
	// String() should contain the cut marker.
	out := h.String()
	if !strings.Contains(out, "cut from the middle") {
		t.Errorf("String() missing cut marker")
	}

	// 4. Multiple Write calls with overflow.
	h = &headTail{}
	overflow := []byte("overflow data that goes to tail eventually")
	for i := 0; i < 1000; i++ {
		n, err := h.Write(overflow)
		if err != nil || n != len(overflow) {
			t.Fatalf("write %d: n=%d err=%v, want n=%d", i, n, err, len(overflow))
		}
	}
	if h.total != 1000*len(overflow) {
		t.Errorf("total=%d, want %d", h.total, 1000*len(overflow))
	}

	// 5. UTF-8 Arabic content at cut boundary.
	h = &headTail{}
	utf8Buf := make([]byte, bashHead+bashTail+500)
	// Fill with Arabic text repeated.
	arabic := []byte("مرحبا بالعالم هذا نص عربي طويل جدا للاختبار. ")
	for i := 0; i < len(utf8Buf); i += len(arabic) {
		copy(utf8Buf[i:], arabic)
	}
	n, err = h.Write(utf8Buf)
	if err != nil || n != len(utf8Buf) {
		t.Fatalf("Write utf8: n=%d err=%v, want n=%d", n, err, len(utf8Buf))
	}
	// Verify no U+FFFD in output (valid UTF-8).
	out = h.String()
	for _, r := range out {
		if r == '\uFFFD' {
			t.Errorf("output contains U+FFFD (invalid UTF-8): %q", out)
			break
		}
	}
}
