package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"nabd/internal/agent"
	"nabd/internal/provider"
)

const (
	maxOutBytes  = 48 * 1024 // what one tool result may cost in context
	maxLines     = 1200
	maxLineRunes = 300 // a minified bundle must not eat the whole budget
	// Read budget derivation (STEP 1/8), written out:
	//   tpmLimit   = 8000 tokens/min  (Groq key, measured live from 7×413)
	//   maxTok     = NABD_MAX_TOKENS  (output reservation; default 1024)
	//   overhead   = 2210 tokens  (MEASURED from two 413 sessions: system
	//                prompt + tool schemas + message framing; the old 450
	//                estimate was wrong by 5×)
	//   bytesPerTok = 2.41  (MEASURED: 4121 read bytes over 1709 tokens
	//                between sessions 203320 and 203954)
	//   roundsPerMin = 2  (tool round + answer round per turn; the TPM cap
	//                is per-minute across all requests, so the per-request
	//                budget divides by the expected request count)
	//   safety     = 0.5
	//   safe_input_per_request = (tpmLimit/maxTok − overhead) / roundsPerMin
	//   defaultMaxRead = safe_input × bytesPerTok × safety
	// The shipped default stays 3072 (live-calibrated) until the derived
	// value passes the disk measurement.
	tpmLimit      = 8000
	maxTokEnv     = "NABD_MAX_TOKENS"
	defaultMaxTok = 1024
	readOverhead  = 2210 // tokens; MEASURED from 413 sessions, not estimated
	bytesPerTok   = 2.41 // MEASURED from session pair, Arabic-heavy content
	readRounds    = 2    // requests per turn (tool + answer)
	readSafety    = 0.5
)

// readMaxTokens mirrors the agent's NABD_MAX_TOKENS resolution so the read
// cap follows the same output reservation. Kept small and local: the tools
// package cannot import agent, and duplicating one env read beats inventing
// a cross-package contract for a single number.
func readMaxTokens() int {
	if v := os.Getenv(maxTokEnv); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 128 && n <= 8192 {
			return n
		}
	}
	return defaultMaxTok
}

// defaultMaxReadDerived derives the read cap from the measured input
// budget. IMPORTANT: the derived value is NOT the default — the shipped
// default is 3072 (live-calibrated) until the derived value passes the
// disk measurement. The derivation is a candidate reachable via
// NABD_MAX_READ.
func defaultMaxReadDerived() int {
	// Per-request input budget: the per-minute TPM cap divided by the
	// expected request count in a turn, minus the measured overhead and the
	// output reservation.
	perReq := tpmLimit/readRounds - readMaxTokens() - readOverhead
	if perReq < 0 {
		perReq = 0
	}
	n := int(float64(perReq) * bytesPerTok * readSafety)
	if n < minMaxRead {
		return minMaxRead
	}
	return n
}

// defaultMaxRead is what NABD_MAX_READ falls back to when unset. Kept at
// the live-calibrated 3072: the derived value (measured constants) still
// needs the disk regression gate before it ships as a default.
func defaultMaxRead() int {
	return 3072
}

// maxReadBytes caps a single read_file call. Read once at startup from
// NABD_MAX_READ so the cap follows the provider's token budget instead of
// being a hardcoded tool constant. Values outside [minMaxRead, maxMaxRead]
// (or non-numeric) are ignored and the default is used: a zero or absurd
// value would otherwise produce an empty read that the model answers with
// false confidence.
const (
	minMaxRead = 512
	maxMaxRead = 1 << 20
)

var maxReadBytes = envMaxRead()

func envMaxRead() int {
	if v := os.Getenv("NABD_MAX_READ"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= minMaxRead && n <= maxMaxRead {
			return n
		}
	}
	return defaultMaxRead()
}

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
	next := 0
	if t.reg != nil {
		trunc, next = t.reg.ConsumeTruncated()
	}
	return agent.Outcome{Text: text, OK: ok, Truncated: trunc, NextOffset: next}, err
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
			// Explicit range + next offset, in lines (the unit read_file's
			// offset param uses), so the model never has to infer it.
			capped = truncTail(from, line-1, total, line)
			break
		}
		if b.Len() > maxOutBytes {
			capped = truncTail(from, line-1, total, line)
			break
		}
		// Byte cap: only emit the line if it still fits under maxReadBytes,
		// so truncation always lands on a line boundary, never mid-line.
		if b.Len()+len(sc.Bytes())+8 > maxReadBytes {
			capped = truncTail(from, line-1, total, line)
			if t.reg != nil {
				t.reg.SetTruncated(line)
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

// truncTail is the single formatter for every truncated read tail. It names
// the range read (start–end), the total, and the exact next offset — all in
// lines, the unit read_file's offset parameter uses — so the model never
// has to infer or convert anything. One function, called from every
// truncation path; a second copy would drift after a month.
func truncTail(start, end, total, nextOffset int) string {
	if end < start {
		end = start
	}
	return fmt.Sprintf(
		"\n[TRUNCATED: قُرئت الأسطر %d-%d من %d؛ للمتابعة استخدم offset=%d]\n"+
			"lines_read=%d  total_lines=%d  next_offset=%d",
		start, end, total, nextOffset,
		end-start+1, total, nextOffset)
}
