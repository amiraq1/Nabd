package tools

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"nabd/internal/agent"
	"nabd/internal/config"
	"nabd/internal/perm"
	"nabd/internal/provider"
)

const (
	maxOutBytes  = 48 * 1024 // what one tool result may cost in context
	maxLines     = 1200
	maxLineRunes = 300 // a minified bundle must not eat the whole budget
)

// defaultMaxRead is what NABD_MAX_READ falls back to when unset. Kept at the
// live-calibrated 3072: the NBD-400 measurement showed that raising the cap
// trades round trips against per-request input, which is the provider-specific
// bound that produced this number (see docs/TECH_DEBT.md, READ_CAP_TURN_COST;
// reproduce with TestReadCapEval / TestReadCapPinsMeasuredTurnCost_NBD401).
//
// An earlier version also carried a derivation of this cap from a tokens-per-
// minute ceiling, a measured overhead and a bytes-per-token ratio. It was
// removed in NBD-403: no production path ever called it (defaultMaxRead
// returned this constant, and the derivation was reachable only from a test),
// and its overhead constant had no reproducible provenance. The derivation is
// recorded in docs/TECH_DEBT.md as history rather than kept here as code that
// looks load-bearing and is not.
func defaultMaxRead() int {
	return 3072
}

// maxReadBytes caps a single read_file call.
//
// It has three sources, resolved once at startup in this order:
//
//  1. NABD_MAX_READ, if set (config file first, environment as the documented
//     fallback). This is the explicit override and it wins over everything,
//     which is why a custom base URL pointed at a metered clone has an escape.
//  2. The provider's own declared ceiling, passed to SetReadCap by cmd/ag from
//     provider.ReadCapper. A Router reports the strictest of its routes.
//  3. defaultMaxRead, when neither applies (tests, and a provider that declares
//     nothing).
//
// Values outside [minMaxRead, maxMaxRead] (or non-numeric) are ignored and the
// next source is used: a zero or absurd value would otherwise produce an empty
// read that the model answers with false confidence.
//
// Note the shape: the cap follows what the provider DECLARES, never what it is
// called. TestReadCapPinsProviderIndependence_NBD401 pins that a provider
// *selection string* cannot move it, and TestProviderReadCaps pins the declared
// values themselves.
const (
	minMaxRead = 512
	maxMaxRead = 1 << 20
)

var maxReadBytes = envMaxRead()

// maxReadExplicit records whether NABD_MAX_READ was set, so that
// SetReadCap knows an operator's explicit choice outranks the provider.
var maxReadExplicit = config.Get("NABD_MAX_READ") != ""

func envMaxRead() int {
	if v := config.Get("NABD_MAX_READ"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= minMaxRead && n <= maxMaxRead {
			return n
		}
	}
	return defaultMaxRead()
}

// SetReadCap applies the ceiling the selected provider declares. It is called
// once at startup, before any session runs.
//
// It never overrides an explicit NABD_MAX_READ, and it rejects a value outside
// the same bounds the override is held to, so a provider cannot accidentally
// widen the cap to something absurd. A non-positive value means "the provider
// declares nothing" and is ignored rather than treated as zero.
func SetReadCap(n int) {
	if maxReadExplicit {
		return
	}
	if n < minMaxRead || n > maxMaxRead {
		return
	}
	maxReadBytes = n
}

// ReadCapBytes reports the cap in force. The loop asks for it through an
// optional interface so a 413 Notice can name the ceiling that was hit.
func (r *Registry) ReadCapBytes() int { return maxReadBytes }

type readFile struct {
	root *Root
	reg  *Registry
}

var _ Classified = readFile{}

func (readFile) Class() perm.Class { return perm.ReadOnly }

func (readFile) Name() string { return "read_file" }

// readMeta is read_file's per-invocation result. Returning it through the
// Outcome (instead of stamping it onto a shared registry slot) is what makes a
// read's metadata ownable: concurrent reads on one Registry can never steal one
// another's count or truncation. read_file itself no longer writes the shared
// linesRead slot; the agent loop threads LinesRead to the next write via
// Registry.SetLinesRead. The truncation slot is retained only for the legacy
// plain-Run path and is drained within RunDetailed, so it cannot leak past the
// call that produced it.
type readMeta struct {
	credit     agent.ReadCredit
	linesRead  int
	truncated  bool
	nextOffset int
}

// RunDetailed lets read_file report truncation and line count through the
// Outcome, so the loop can journal a read_record event when the byte cap cut
// the file short.
func (t readFile) RunDetailed(ctx context.Context, raw json.RawMessage) (agent.Outcome, error) {
	text, meta, ok, err := t.run(ctx, raw)
	if err != nil || ctx.Err() != nil {
		// A failed or cancelled read must not leave partial metadata for a
		// later unrelated call to inherit.
		if t.reg != nil {
			t.reg.ClearReadState()
		}
		return agent.Outcome{Text: text, OK: ok}, err
	}
	// The truncation flag lives in the Outcome (per-invocation). Drain the
	// legacy slot here — its recovered value is intentionally discarded in
	// favour of meta, so a concurrent reader can never swap flags via the slot.
	if t.reg != nil {
		t.reg.ConsumeTruncated()
	}
	return agent.Outcome{
		Text:       text,
		OK:         ok,
		Truncated:  meta.truncated,
		NextOffset: meta.nextOffset,
		LinesRead:  meta.linesRead,
		ReadCredit: meta.credit,
	}, nil
}

func (readFile) Spec() provider.ToolSpec {
	return spec("read_file",
		"Read a text file. Lines are numbered. Use offset and limit for long files.",
		`{"type":"object","properties":{
			"path":{"type":"string","description":"relative path from the project root"},
			"offset":{"type":"integer","description":"first line (starts at 1)"},
			"limit":{"type":"integer","description":"number of lines"}},
		 "required":["path"]}`)
}

// Run wraps the read implementation so that any error path — including the
// plain Registry.Run path that never reaches RunDetailed — clears pending
// read metadata. A failed read must not contaminate a later unrelated call
// with stale linesRead/truncation state. The per-invocation line count and
// truncation live in the Outcome (see RunDetailed); this plain path returns
// text only and does not touch the shared slots.
func (t readFile) Run(ctx context.Context, raw json.RawMessage) (string, bool, error) {
	text, _, ok, err := t.run(ctx, raw)
	if err != nil && t.reg != nil {
		t.reg.ClearReadState()
	}
	return text, ok, err
}

func (t readFile) run(_ context.Context, raw json.RawMessage) (string, readMeta, bool, error) {
	var a struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", readMeta{}, false, fmt.Errorf("invalid args: %w", err)
	}

	p, err := t.root.Resolve(a.Path)
	if err != nil {
		return "", readMeta{}, false, err
	}
	fi, err := os.Stat(p)
	if err != nil {
		return "", readMeta{}, false, err
	}
	if fi.IsDir() {
		return "", readMeta{}, false, fmt.Errorf("%s is a directory · use glob", t.root.Rel(p))
	}

	f, err := os.Open(p)
	if err != nil {
		return "", readMeta{}, false, err
	}
	defer f.Close()

	// Binary files are refused rather than mangled: a NUL byte in the
	// first block is the only reliable cheap signal.
	head := make([]byte, 8192)
	n, _ := f.Read(head)
	if strings.IndexByte(string(head[:n]), 0) >= 0 {
		return "", readMeta{}, false, fmt.Errorf("%s is binary (%d bytes)", t.root.Rel(p), fi.Size())
	}
	if _, err := f.Seek(0, 0); err != nil {
		return "", readMeta{}, false, err
	}

	// Compute full-file SHA-256 hash at read time for composite key provenance (NBD-034).
	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return "", readMeta{}, false, err
	}
	fileHash := hex.EncodeToString(hasher.Sum(nil))
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", readMeta{}, false, err
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
		return "", readMeta{}, false, err
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	var meta readMeta
	line, shown, capped := 0, 0, ""
	for sc.Scan() {
		line++
		if line < from {
			continue
		}
		if shown >= limit {
			// Explicit range + next offset, in lines (the unit read_file's
			// offset param uses), so the model never has to infer it.
			capped = TruncTail(from, line-1, total, line)
			break
		}
		if b.Len() > maxOutBytes {
			capped = TruncTail(from, line-1, total, line)
			break
		}
		// Byte cap: only emit the line if it still fits under maxReadBytes,
		// so truncation always lands on a line boundary, never mid-line.
		if b.Len()+len(sc.Bytes())+8 > maxReadBytes {
			if shown == 0 && line == from {
				// The line itself exceeds the cap. It is emitted clipped and
				// marked, and next_offset skips past it — the remainder of
				// this line is NOT reachable (the tool has no byte offset),
				// so the marker says so explicitly rather than let the model
				// believe it saw the whole file.
				fmt.Fprintf(&b, "%d|%s [LINE_TRUNCATED: line longer than maxReadBytes=%d; remainder of this line is not readable with this tool]\n", line, clip(sc.Text(), maxLineRunes), maxReadBytes)
				shown++
				capped = TruncTail(from, line, total, line+1)
				meta.truncated = true
				meta.nextOffset = line + 1
				if t.reg != nil {
					t.reg.SetTruncated(line + 1)
				}
				break
			}
			capped = TruncTail(from, line-1, total, line)
			meta.truncated = true
			meta.nextOffset = line
			if t.reg != nil {
				t.reg.SetTruncated(line)
			}
			break
		}
		fmt.Fprintf(&b, "%d|%s\n", line, clip(sc.Text(), maxLineRunes))
		shown++
	}
	if err := sc.Err(); err != nil {
		return "", readMeta{}, false, err
	}

	if shown == 0 {
		meta.credit = agent.ReadCredit{
			Path:      p,
			Hash:      fileHash,
			Offset:    from,
			Limit:     limit,
			LinesRead: 0,
		}
		if line == 0 {
			return fmt.Sprintf("%s is empty", t.root.Rel(p)), meta, true, nil
		}
		return fmt.Sprintf("no lines at offset=%d · file has %d lines", from, line), meta, true, nil
	}
	if capped != "" {
		b.WriteString(capped + "\n")
	}
	meta.linesRead = shown
	meta.credit = agent.ReadCredit{
		Path:      p,
		Hash:      fileHash,
		Offset:    from,
		Limit:     limit,
		LinesRead: shown,
	}
	return b.String(), meta, true, nil
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + fmt.Sprintf(" …[+%d]", len(r)-n)
}

// TruncTail is the single formatter for every truncated read tail. It names
// the range read (start–end), the total, and the exact next offset — all in
// lines, the unit read_file's offset parameter uses — so the model never
// has to infer or convert anything. One function, called from every
// truncation path; a second copy would drift after a month.
func TruncTail(start, end, total, nextOffset int) string {
	if end < start {
		end = start
	}
	return fmt.Sprintf(
		"\n[TRUNCATED: read lines %d-%d of %d; continue with offset=%d]\n"+
			"lines_read=%d  total_lines=%d  next_offset=%d",
		start, end, total, nextOffset,
		end-start+1, total, nextOffset)
}
