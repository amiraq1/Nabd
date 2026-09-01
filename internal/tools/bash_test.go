package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

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
