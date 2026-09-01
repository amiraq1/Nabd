package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"nabd/internal/agent"
	"nabd/internal/provider"
)

const (
	maxOutBytes  = 48 * 1024 // what one tool result may cost in context
	maxLines     = 1200
	maxLineRunes = 300 // a minified bundle must not eat the whole budget
	// maxBytes caps a single read in bytes so a large file cannot fill the
	// context budget by itself (the Groq TPM ceiling is 8000 tokens).
	// DESIGN_ASSUMPTION, calibrated by live tests:
	//   - 16 KiB read → 413 (Requested 8016)
	//   - 8 KiB read, model read twice → 413 (Requested 9725)
	// Two reads plus system+conversation must stay under 8000, so a single
	// read is capped at 3 KiB ≈ ~1.5k tokens. Truncation is at a line
	// boundary. Lower this when the key's TPM ceiling rises.
	maxBytes = 3 * 1024
)

type readFile struct {
	root *Root
	reg  *Registry
}

func (readFile) Name() string { return "read_file" }

// RunDetailed lets read_file report truncation through the Outcome, so the
// loop can journal a read_record event when the byte cap cut the file.
func (t readFile) RunDetailed(ctx context.Context, raw json.RawMessage) (agent.Outcome, error) {
	text, ok, err := t.Run(ctx, raw)
	trunc := false
	if t.reg != nil {
		trunc = t.reg.ConsumeTruncated()
	}
	return agent.Outcome{Text: text, OK: ok, Truncated: trunc}, err
}

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

	// Count the file's real line count up front: the truncation tail must
	// say "stopped at line N of M" with the true M, not the number of lines
	// the loop managed to read before the cap.
	total := 0
	tc := bufio.NewScanner(f)
	tc.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	for tc.Scan() {
		total++
	}
	if _, err := f.Seek(0, 0); err != nil {
		return "", false, err
	}

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
		// Byte cap: only emit the line if it still fits under maxBytes,
		// so truncation always lands on a line boundary, never mid-line.
		if b.Len()+len(sc.Bytes())+8 > maxBytes {
			capped = fmt.Sprintf(
				"[TRUNCATED: stopped at line %d of %d; use offset=%d to continue]",
				line-1, total, line)
			if t.reg != nil {
				t.reg.SetTruncated()
			}
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
