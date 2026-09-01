package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"nabd/internal/provider"
)

const (
	maxOutBytes  = 48 * 1024 // what one tool result may cost in context
	maxLines     = 1200
	maxLineRunes = 300 // a minified bundle must not eat the whole budget
)

type readFile struct {
	root *Root
	reg  *Registry
}

func (readFile) Name() string { return "read_file" }

func (readFile) Spec() provider.ToolSpec {
	return spec("read_file",
		"اقرأ ملفًا نصيًا. الأسطر مرقّمة. استخدم offset وlimit للملفات الطويلة.",
		`{"type":"object","properties":{
			"path":{"type":"string","description":"مسار نسبي من جذر المشروع"},
			"offset":{"type":"integer","description":"أول سطر (يبدأ من ١)"},
			"limit":{"type":"integer","description":"عدد الأسطر"}},
		 "required":["path"]}`)
}

func (t readFile) Run(_ context.Context, raw json.RawMessage) (string, bool, error) {
	var a struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", false, fmt.Errorf("وسائط غير صالحة: %w", err)
	}

	p, err := t.root.Resolve(a.Path)
	if err != nil {
		return "", false, err
	}
	fi, err := os.Stat(p)
	if err != nil {
		return "", false, err
	}
	if fi.IsDir() {
		return "", false, fmt.Errorf("%s مجلد · استخدم glob", t.root.Rel(p))
	}

	f, err := os.Open(p)
	if err != nil {
		return "", false, err
	}
	defer f.Close()

	// Binary files are refused rather than mangled: a NUL byte in the
	// first block is the only reliable cheap signal.
	head := make([]byte, 8192)
	n, _ := f.Read(head)
	if strings.IndexByte(string(head[:n]), 0) >= 0 {
		return "", false, fmt.Errorf("%s ملف ثنائي (%d بايت)", t.root.Rel(p), fi.Size())
	}
	if _, err := f.Seek(0, 0); err != nil {
		return "", false, err
	}

	from := a.Offset
	if from < 1 {
		from = 1
	}
	limit := a.Limit
	if limit <= 0 || limit > maxLines {
		limit = maxLines
	}

	var b strings.Builder
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	line, shown, capped := 0, 0, ""
	for sc.Scan() {
		line++
		if line < from {
			continue
		}
		if shown >= limit {
			capped = fmt.Sprintf("… توقّف عند السطر %d · limit=%d", line-1, limit)
			break
		}
		if b.Len() > maxOutBytes {
			capped = fmt.Sprintf("… توقّف عند السطر %d · بلغ حدّ الحجم", line-1)
			break
		}
		fmt.Fprintf(&b, "%d|%s\n", line, clip(sc.Text(), maxLineRunes))
		shown++
	}
	if err := sc.Err(); err != nil {
		return "", false, err
	}

	if shown == 0 {
		if line == 0 {
			return fmt.Sprintf("%s فارغ", t.root.Rel(p)), true, nil
		}
		return fmt.Sprintf("لا سطر عند offset=%d · الملف %d سطرًا", from, line), true, nil
	}
	if capped != "" {
		b.WriteString(capped + "\n")
	}
	if t.reg != nil {
		t.reg.SetLinesRead(shown)
	}
	return b.String(), true, nil
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + fmt.Sprintf(" …[+%d]", len(r)-n)
}
