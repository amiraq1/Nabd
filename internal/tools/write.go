// Package tools: write.go holds the only two tools that change the disk.
// Every mutation is shadowed first, written atomically, then re-read and
// compared by hash. A write that cannot be proven did not happen.
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"nabd/internal/provider"
	"nabd/internal/snap"
)

const (
	maxWriteBytes = 1 << 20
	maxEditBytes  = 2 << 20
)

// Edit is what the shadow recorded around one mutation, kept in order so a
// later /undo has something to walk backwards through.
type Edit struct {
	Tool   string
	Rel    string
	Before snap.State
	After  snap.State
}

type editLog struct {
	mu sync.Mutex
	l  []Edit
}

func (e *editLog) add(x Edit) {
	e.mu.Lock()
	e.l = append(e.l, x)
	e.mu.Unlock()
}

func (e *editLog) all() []Edit {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]Edit(nil), e.l...)
}

// commit is the shared tail of both tools: shadow, write, verify, log.
func commit(root *Root, sh *snap.Shadow, log *editLog, tool, abs string, data []byte) (snap.State, snap.State, error) {
	before, err := sh.Capture(abs)
	if err != nil {
		return before, snap.State{}, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return before, snap.State{}, err
	}
	mode := os.FileMode(0o644)
	if !before.Absent && before.Mode != 0 {
		mode = before.Mode
	}
	if err := snap.WriteAtomic(abs, data, mode); err != nil {
		return before, snap.State{}, err
	}
	want, err := sh.CaptureBytes(abs, data, mode)
	if err != nil {
		return before, snap.State{}, err
	}
	after, err := sh.Capture(abs)
	if err != nil {
		return before, after, err
	}
	if !snap.Unchanged(want, after) {
		return before, after, errors.New("الكتابة لم تثبت على القرص كما هي")
	}
	log.add(Edit{Tool: tool, Rel: root.Rel(abs), Before: before, After: after})
	return before, after, nil
}

type writeFile struct {
	root *Root
	sh   *snap.Shadow
	log  *editLog
}

func (writeFile) Name() string { return "write_file" }

func (writeFile) Spec() provider.ToolSpec {
	return spec("write_file",
		"يكتب ملفًا كاملًا داخل المشروع، وينشئ المجلدات الناقصة. المحتوى القديم يُستبدل بالكامل، فاقرأ الملف أولًا إن كان موجودًا.",
		`{"type":"object","properties":{
			"path": {"type": "string", "description": "مسار نسبي داخل المشروع"},
			"content": {"type": "string", "description": "المحتوى الكامل الجديد"}
		}, "required":["path", "content"]}`)
}

func (w writeFile) Run(ctx context.Context, raw json.RawMessage) (string, bool, error) {
	var a struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", false, fmt.Errorf("وسائط غير صالحة: %w", err)
	}
	if len(a.Content) > maxWriteBytes {
		return "", false, fmt.Errorf("المحتوى %d بايت، والحد %d", len(a.Content), maxWriteBytes)
	}
	abs, err := w.root.Resolve(a.Path)
	if err != nil {
		return "", false, err
	}
	before, after, err := commit(w.root, w.sh, w.log, "write_file", abs, []byte(a.Content))
	if err != nil {
		return "", false, err
	}
	verb := "استُبدل"
	if before.Absent {
		verb = "أُنشئ"
	}
	return fmt.Sprintf("%s %s (%d بايت، %d سطرًا)", verb, w.root.Rel(abs), after.Size, linesIn(a.Content)), true, nil
}

type editFile struct {
	root *Root
	sh   *snap.Shadow
	log  *editLog
}

func (editFile) Name() string { return "edit_file" }

func (editFile) Spec() provider.ToolSpec {
	return spec("edit_file",
		"يستبدل نصًا بنص داخل ملف موجود. النص القديم يجب أن يكون فريدًا في الملف، أو مرّر all=true لاستبدال كل المواضع.",
		`{"type":"object","properties":{
			"path": {"type": "string", "description": "مسار نسبي داخل المشروع"},
			"old": {"type": "string", "description": "النص المطلوب استبداله، حرفيًا"},
			"new": {"type": "string", "description": "النص البديل"},
			"all": {"type": "boolean", "description": "استبدال كل المواضع"}
		}, "required":["path", "old", "new"]}`)
}

func (w editFile) Run(ctx context.Context, raw json.RawMessage) (string, bool, error) {
	var a struct {
		Path string `json:"path"`
		Old  string `json:"old"`
		New  string `json:"new"`
		All  bool   `json:"all"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", false, fmt.Errorf("وسائط غير صالحة: %w", err)
	}
	if a.Old == "" {
		return "", false, errors.New("النص القديم فارغ؛ استخدم write_file لملف جديد")
	}
	abs, err := w.root.Resolve(a.Path)
	if err != nil {
		return "", false, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return "", false, err
	}
	if st.Size() > maxEditBytes {
		return "", false, fmt.Errorf("الملف %d بايت، والحد %d", st.Size(), maxEditBytes)
	}
	src, err := os.ReadFile(abs)
	if err != nil {
		return "", false, err
	}
	if hasNUL(src) {
		return "", false, errors.New("ملف ثنائي")
	}
	n := strings.Count(string(src), a.Old)
	switch {
	case n == 0:
		return "", false, errors.New("لم يُعثر على النص القديم في " + w.root.Rel(abs))
	case n > 1 && !a.All:
		return "", false, fmt.Errorf("النص القديم ورد %d مرات؛ وسّع المقطع ليصبح فريدًا أو مرّر all=true", n)
	}
	reps := n
	var out string
	if a.All {
		out = strings.ReplaceAll(string(src), a.Old, a.New)
	} else {
		out = strings.Replace(string(src), a.Old, a.New, 1)
		reps = 1
	}
	if _, _, err := commit(w.root, w.sh, w.log, "edit_file", abs, []byte(out)); err != nil {
		return "", false, err
	}
	return fmt.Sprintf("عُدّل %s (%d استبدال، %d سطرًا ← %d)",
		w.root.Rel(abs), reps, linesIn(string(src)), linesIn(out)), true, nil
}

func linesIn(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

func hasNUL(b []byte) bool {
	if len(b) > 8000 {
		b = b[:8000]
	}
	return bytes.IndexByte(b, 0) >= 0
}
