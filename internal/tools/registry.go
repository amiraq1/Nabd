package tools

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	shadowstore "github.com/amiraq1/nabd/internal/shadow"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

const (
	maxOutputBytes = 4096
	maxReadBytes   = 512 << 10
	maxEditBytes   = 2 << 20
	maxListEntries = 1000
)

type ApprovalFunc func(toolName, summary string) bool

type ToolSpec struct {
	Name        string
	Description string
	Parameters  json.RawMessage
	// ReadOnly marks tools that cannot modify the workspace or execute
	// external code. The zero value is false, so every new tool requires
	// approval unless its author explicitly declares it read-only.
	ReadOnly bool
	Execute  func(ctx context.Context, args json.RawMessage) (string, error)
}

type Registry struct {
	workDir  string
	root     *os.Root
	shadow   *shadowstore.Store
	approve  ApprovalFunc
	handlers map[string]ToolSpec
}

func NewRegistry(workDir string, approve ApprovalFunc) (*Registry, error) {
	absRoot, err := filepath.Abs(workDir)
	if err != nil {
		return nil, err
	}
	if resolved, err := filepath.EvalSymlinks(absRoot); err == nil {
		absRoot = resolved
	}
	root, err := os.OpenRoot(absRoot)
	if err != nil {
		return nil, fmt.Errorf("open workspace root: %w", err)
	}
	store, err := shadowstore.Open(root)
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	r := &Registry{
		workDir:  absRoot,
		root:     root,
		shadow:   store,
		approve:  approve,
		handlers: make(map[string]ToolSpec),
	}
	r.registerDefaults()
	return r, nil
}

// Close releases the workspace handle. The registry must not be used afterwards.
func (r *Registry) Close() error {
	if r.root == nil {
		return nil
	}
	err := r.root.Close()
	r.root = nil
	return err
}

// OpenAITools returns tool definitions sorted alphabetically to preserve KV-cache.
func (r *Registry) OpenAITools() []map[string]any {
	names := make([]string, 0, len(r.handlers))
	for name := range r.handlers {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]map[string]any, 0, len(names))
	for _, name := range names {
		spec := r.handlers[name]
		var params any
		if len(spec.Parameters) > 0 {
			_ = json.Unmarshal(spec.Parameters, &params)
		} else {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        spec.Name,
				"description": spec.Description,
				"parameters":  params,
			},
		})
	}
	return out
}

// RequiresApproval reports whether a tool must be approved before running.
// Unknown tools and every tool that is not explicitly read-only require
// approval.
func (r *Registry) RequiresApproval(name string) bool {
	spec, ok := r.handlers[name]
	if !ok {
		return true
	}
	return !spec.ReadOnly
}

// Has reports whether a tool is registered.
func (r *Registry) Has(name string) bool {
	_, ok := r.handlers[name]
	return ok
}

// Disable removes tools from the registry. Disabled tools are neither
// advertised nor executable.
func (r *Registry) Disable(names ...string) {
	for _, name := range names {
		delete(r.handlers, name)
	}
}

// resolveSafePath validates a workspace-relative path and returns it in the
// form expected by os.Root. File operations use the root handle, so the
// containment check is enforced at open time, not only here.
func (r *Registry) resolveSafePath(relPath string) (string, error) {
	if strings.TrimSpace(relPath) == "" {
		return "", fmt.Errorf("empty path is not allowed")
	}
	if strings.ContainsRune(relPath, 0) {
		return "", fmt.Errorf("invalid path: contains NUL byte")
	}
	cleaned := filepath.Clean(relPath)
	if filepath.IsAbs(cleaned) || filepath.VolumeName(cleaned) != "" {
		return "", fmt.Errorf("absolute paths are not allowed: %s", relPath)
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path outside workspace not allowed: %s", relPath)
	}
	if isSensitivePath(cleaned) {
		return "", fmt.Errorf("access to sensitive path denied: %s", relPath)
	}

	// Defense in depth: os.Root rejects escaping symlinks at open time, but we
	// still check the resolved path so symlinks to sensitive files are denied.
	abs := filepath.Join(r.workDir, cleaned)
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		relResolved, err := filepath.Rel(r.workDir, resolved)
		if err != nil || relResolved == ".." || strings.HasPrefix(relResolved, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("symlink points outside workspace: %s", relPath)
		}
		if isSensitivePath(relResolved) {
			return "", fmt.Errorf("access to sensitive path denied: %s", relPath)
		}
	}

	return cleaned, nil
}

// isSensitivePath denies credentials, VCS internals and the reserved .nabd
// directory. The check is intentionally case-insensitive so it also holds on
// case-insensitive filesystems such as Android shared storage.
func isSensitivePath(rel string) bool {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for _, part := range parts {
		switch strings.ToLower(part) {
		case ".nabd", ".git":
			return true
		}
	}

	base := strings.ToLower(parts[len(parts)-1])
	if strings.HasPrefix(base, ".nabd-tmp-") {
		return true
	}
	switch {
	case base == ".env":
		return true
	case strings.HasPrefix(base, ".env."):
		switch base {
		case ".env.example", ".env.sample", ".env.template", ".env.dist":
			return false
		}
		return true
	case strings.HasPrefix(base, "id_") && !strings.HasSuffix(base, ".pub"):
		return true
	}

	switch strings.ToLower(filepath.Ext(base)) {
	case ".pem", ".key", ".p12", ".pfx", ".jks", ".keystore":
		return true
	}
	return false
}

// FileDigest returns the SHA-256 of a file inside the workspace. The approval
// UI uses it to confirm that the file did not change between the preview and
// the approved edit.
func (r *Registry) FileDigest(relPath string) (string, error) {
	rel, err := r.resolveSafePath(relPath)
	if err != nil {
		return "", err
	}
	f, err := r.root.Open(rel)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (r *Registry) readFile(rel string) (string, error) {
	info, err := r.root.Stat(rel)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory", rel)
	}
	f, err := r.root.Open(rel)
	if err != nil {
		return "", err
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxReadBytes+1))
	if err != nil {
		return "", err
	}
	truncated := len(data) > maxReadBytes
	if truncated {
		data = data[:maxReadBytes]
	}
	out := string(data)
	if truncated {
		out += fmt.Sprintf("\n...[truncated at %d bytes]", maxReadBytes)
	}
	return out, nil
}

func (r *Registry) listDir(rel string) (string, error) {
	f, err := r.root.Open(rel)
	if err != nil {
		return "", err
	}
	defer f.Close()

	entries, err := f.ReadDir(-1)
	if err != nil {
		return "", err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	var b strings.Builder
	shown, hidden := 0, 0
	for _, e := range entries {
		if isSensitivePath(filepath.Join(rel, e.Name())) {
			continue
		}
		if shown >= maxListEntries {
			hidden++
			continue
		}
		typ := "F"
		if e.IsDir() {
			typ = "D"
		}
		b.WriteString(fmt.Sprintf("%s  %s\n", typ, e.Name()))
		shown++
	}
	if hidden > 0 {
		b.WriteString(fmt.Sprintf("... [%d more entries]\n", hidden))
	}
	return b.String(), nil
}

// Undo restores the most recent non-undone shadow entry for relPath (or the
// most recent entry overall when relPath is empty). It refuses if the file
// changed since the edit or if the shadow copy is corrupted.
func (r *Registry) Undo(relPath string) (string, error) {
	var target string
	if strings.TrimSpace(relPath) != "" {
		var err error
		target, err = r.resolveSafePath(relPath)
		if err != nil {
			return "", err
		}
	}

	entry, backup, err := r.shadow.Latest(target)
	if err != nil {
		return "", err
	}

	currentPath, err := r.resolveSafePath(entry.Path)
	if err != nil {
		return "", err
	}
	info, err := r.root.Lstat(currentPath)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("refusing to undo a symlink: %s", entry.Path)
	}

	current, err := r.root.ReadFile(currentPath)
	if err != nil {
		return "", err
	}
	if hashBytes(current) != entry.SHA256After {
		return "", fmt.Errorf("refusing undo: %s changed since the edit", entry.Path)
	}

	if err := r.atomicWrite(currentPath, backup, os.FileMode(entry.Mode)); err != nil {
		return "", err
	}
	if err := r.shadow.MarkUndone(entry.ID); err != nil {
		return "", fmt.Errorf("restored %s but could not update shadow metadata: %w", entry.Path, err)
	}
	return fmt.Sprintf("Restored %s from shadow %s", entry.Path, entry.ID), nil
}

// atomicWrite writes data to a temporary file in the target directory, syncs
// it, and renames it over the destination. All operations stay inside root.
func (r *Registry) atomicWrite(rel string, data []byte, mode os.FileMode) error {
	tmp := filepath.Join(filepath.Dir(rel), ".nabd-tmp-"+randomHex(8))
	f, err := r.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}

	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		_ = r.root.Remove(tmp)
		return fmt.Errorf("write temporary file: %w", err)
	}

	if err := r.root.Rename(tmp, rel); err != nil {
		_ = r.root.Remove(tmp)
		return fmt.Errorf("replace %s: %w", rel, err)
	}
	return nil
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

// Run executes a tool. Tools that are not read-only are refused unless an
// approver is configured and grants the request, so callers without an
// interactive approval path fail closed.
func (r *Registry) Run(ctx context.Context, name string, rawArgs json.RawMessage) string {
	return r.run(ctx, name, rawArgs, false)
}

// RunApproved executes a tool after the caller has already obtained explicit
// approval. Callers must only use it immediately after their own approval
// step; Run remains the fail-closed entry point for everyone else.
func (r *Registry) RunApproved(ctx context.Context, name string, rawArgs json.RawMessage) string {
	return r.run(ctx, name, rawArgs, true)
}

func (r *Registry) run(ctx context.Context, name string, rawArgs json.RawMessage, approved bool) string {
	spec, ok := r.handlers[name]
	if !ok {
		return fmt.Sprintf("Error: unknown tool %q", name)
	}
	if !spec.ReadOnly && !approved {
		if r.approve == nil {
			return fmt.Sprintf("Error: tool %q requires approval, but no approver is configured", name)
		}
		if !r.approve(name, string(rawArgs)) {
			return "Action rejected by user."
		}
	}
	out, err := spec.Execute(ctx, rawArgs)
	if err != nil {
		return fmt.Sprintf("Error: %v\nOutput:\n%s", err, truncateOutput(out))
	}
	return truncateOutput(out)
}

func (r *Registry) registerDefaults() {
	r.handlers["edit_file"] = ToolSpec{
		Name:        "edit_file",
		Description: "Replace an exact block of text in a file. Validates and formats Go code before saving.",
		ReadOnly:    false,
		Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Relative file path"},"old_text":{"type":"string","description":"Exact existing block to replace"},"new_text":{"type":"string","description":"Replacement block"}},"required":["path","old_text","new_text"]}`),
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			var p struct {
				Path    string `json:"path"`
				OldText string `json:"old_text"`
				NewText string `json:"new_text"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return "", err
			}
			if p.OldText == "" {
				return "", fmt.Errorf("old_text cannot be empty")
			}
			if p.OldText == p.NewText {
				return "", fmt.Errorf("old_text and new_text are identical")
			}

			rel, err := r.resolveSafePath(p.Path)
			if err != nil {
				return "", err
			}
			info, err := r.root.Lstat(rel)
			if err != nil {
				return "", err
			}
			if info.IsDir() {
				return "", fmt.Errorf("%s is a directory", p.Path)
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return "", fmt.Errorf("editing symlinks is not supported: %s", p.Path)
			}
			if info.Size() > maxEditBytes {
				return "", fmt.Errorf("file is too large to edit (%d bytes; limit %d)", info.Size(), maxEditBytes)
			}

			contentBytes, err := r.root.ReadFile(rel)
			if err != nil {
				return "", err
			}

			hasCRLF := bytes.Contains(contentBytes, []byte("\r\n"))
			content := strings.ReplaceAll(string(contentBytes), "\r\n", "\n")
			oldNorm := strings.ReplaceAll(p.OldText, "\r\n", "\n")
			newNorm := strings.ReplaceAll(p.NewText, "\r\n", "\n")
			if oldNorm == newNorm {
				return "", fmt.Errorf("old_text and new_text are identical after line-ending normalization")
			}

			count := strings.Count(content, oldNorm)
			if count == 0 {
				return "", fmt.Errorf("old_text not found in %s", p.Path)
			}
			if count != 1 {
				return "", fmt.Errorf("old_text appears %d times; include more surrounding lines to make it unique", count)
			}

			updatedBytes := []byte(strings.Replace(content, oldNorm, newNorm, 1))

			if strings.HasSuffix(rel, ".go") {
				formatted, err := format.Source(updatedBytes)
				if err != nil {
					return "", fmt.Errorf("edit rejected due to Go syntax error:\n%v", err)
				}
				updatedBytes = formatted
			}
			if hasCRLF {
				updatedBytes = bytes.ReplaceAll(updatedBytes, []byte("\n"), []byte("\r\n"))
			}
			if bytes.Equal(updatedBytes, contentBytes) {
				return "", fmt.Errorf("edit would not change %s", p.Path)
			}

			entry, err := r.shadow.Backup(rel, contentBytes, info.Mode(), updatedBytes)
			if err != nil {
				return "", err
			}
			if err := r.atomicWrite(rel, updatedBytes, info.Mode()); err != nil {
				_ = r.shadow.MarkUndone(entry.ID)
				return "", err
			}
			return fmt.Sprintf("Successfully updated %s (shadow %s)", p.Path, entry.ID), nil
		},
	}

	r.handlers["go_verify"] = ToolSpec{
		Name:        "go_verify",
		Description: "Run go test and go vet with Termux-safe memory limits.",
		ReadOnly:    false,
		Parameters:  json.RawMessage(`{"type":"object","properties":{"pkg":{"type":"string","description":"Package pattern, default ./..."}}}`),
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			var p struct {
				Pkg string `json:"pkg"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return "", err
			}
			pkg, err := r.validatePkgPattern(p.Pkg)
			if err != nil {
				return "", err
			}
			return r.runGoVerify(ctx, pkg, 90*time.Second)
		},
	}

	r.handlers["read_file"] = ToolSpec{
		Name:        "read_file",
		Description: "Read the contents of a file inside the workspace.",
		ReadOnly:    true,
		Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Relative file path"}},"required":["path"]}`),
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			var p struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return "", err
			}
			rel, err := r.resolveSafePath(p.Path)
			if err != nil {
				return "", err
			}
			return r.readFile(rel)
		},
	}

	r.handlers["list_dir"] = ToolSpec{
		Name:        "list_dir",
		Description: "List directory contents inside the workspace.",
		ReadOnly:    true,
		Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Relative directory path (default .)"}}}`),
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			var p struct {
				Path string `json:"path"`
			}
			_ = json.Unmarshal(args, &p)
			if strings.TrimSpace(p.Path) == "" {
				p.Path = "."
			}
			rel, err := r.resolveSafePath(p.Path)
			if err != nil {
				return "", err
			}
			return r.listDir(rel)
		},
	}

	r.handlers["mcp_call"] = ToolSpec{
		Name:        "mcp_call",
		Description: "Call a tool on an external MCP server via stdio. Starts the server process, calls the tool, then closes it.",
		ReadOnly:    false,
		Parameters:  json.RawMessage(`{"type":"object","properties":{"command":{"type":"string","description":"Command to start the MCP server (e.g. nabd-mcp)"},"tool":{"type":"string","description":"Name of the tool to call on the MCP server"},"arguments":{"type":"object","description":"Arguments to pass to the tool"}},"required":["command","tool"]}`),
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			var p struct {
				Command   string                 `json:"command"`
				Tool      string                 `json:"tool"`
				Arguments map[string]interface{} `json:"arguments"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return "", err
			}
			if p.Command == "" || p.Tool == "" {
				return "", fmt.Errorf("command and tool are required")
			}

			tCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()

			stdioTransport := transport.NewStdio(p.Command, nil)
			c := client.NewClient(stdioTransport)
			if err := c.Start(tCtx); err != nil {
				return "", fmt.Errorf("failed to start MCP server: %w", err)
			}
			defer c.Close()

			result, err := c.CallTool(tCtx, mcp.CallToolRequest{
				Params: mcp.CallToolParams{
					Name:      p.Tool,
					Arguments: p.Arguments,
				},
			})
			if err != nil {
				return "", fmt.Errorf("mcp call failed: %w", err)
			}

			var out strings.Builder
			for _, content := range result.Content {
				if tc, ok := content.(mcp.TextContent); ok {
					out.WriteString(tc.Text)
					out.WriteString("\n")
				}
			}
			if out.Len() == 0 {
				return "(no text content returned)", nil
			}
			return out.String(), nil
		},
	}
}

// validatePkgPattern accepts only package patterns that stay inside the
// workspace. Module paths ("std", "github.com/...") are rejected so go_verify
// cannot be pointed at packages outside the project.
func (r *Registry) validatePkgPattern(pattern string) (string, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return "./...", nil
	}
	if strings.HasPrefix(pattern, "-") {
		return "", fmt.Errorf("invalid package pattern %q: flags are not allowed", pattern)
	}
	switch pattern {
	case ".", "./":
		return ".", nil
	case "./...":
		return pattern, nil
	}
	if !strings.HasPrefix(pattern, "./") {
		return "", fmt.Errorf("package pattern must be workspace-relative (for example ./... or ./pkg)")
	}

	rel := strings.TrimSuffix(pattern, "/...")
	rel = strings.TrimPrefix(rel, "./")
	if rel == "" {
		return "", fmt.Errorf("invalid package pattern %q", pattern)
	}
	if _, err := r.resolveSafePath(rel); err != nil {
		return "", err
	}
	return pattern, nil
}

// runGoVerify executes go test with a hard timeout and a structured summary.
// Test failures are normal results, not tool errors.
func (r *Registry) runGoVerify(ctx context.Context, pkg string, timeout time.Duration) (string, error) {
	tCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(tCtx, "go", "test", "-p", "2", "-vet=all", pkg)
	cmd.Dir = r.workDir
	cmd.Env = buildVerifyEnv()
	cmd.WaitDelay = 2 * time.Second
	configureProcessTree(cmd)

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()

	status := "PASS"
	if err != nil {
		status = "FAIL"
	}
	if tCtx.Err() == context.DeadlineExceeded {
		status = "FAIL (timeout)"
	}
	if err != nil && !isGoTestFailure(tCtx, err) {
		return "", err
	}
	return summarizeGoTest(status, out.String(), err), nil
}

func isGoTestFailure(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return true
	}
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr)
}

const (
	maxSummarySignals = 5
	maxSummaryTail    = 12
	maxSummaryLine    = 180
)

// summarizeGoTest returns a small, structured result instead of raw logs: a
// status line, the first failure signals, and the tail of the output.
func summarizeGoTest(status, output string, err error) string {
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")

	var b strings.Builder
	b.WriteString(status)
	if err != nil {
		b.WriteString(" (")
		b.WriteString(clipSummary(err.Error(), maxSummaryLine))
		b.WriteString(")")
	}
	b.WriteString("\n")

	signals := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "--- FAIL:") ||
			strings.Contains(trimmed, "build failed") ||
			strings.Contains(trimmed, "cannot find") ||
			strings.Contains(trimmed, "undefined:") ||
			strings.Contains(trimmed, "panic:") {
			if signals >= maxSummarySignals {
				break
			}
			b.WriteString("  ")
			b.WriteString(clipSummary(trimmed, maxSummaryLine))
			b.WriteString("\n")
			signals++
		}
	}

	b.WriteString("--- tail ---\n")
	if len(lines) == 1 && lines[0] == "" {
		b.WriteString("(no output)\n")
		return b.String()
	}
	start := len(lines) - maxSummaryTail
	if start < 0 {
		start = 0
	}
	for _, line := range lines[start:] {
		b.WriteString(clipSummary(line, maxSummaryLine))
		b.WriteString("\n")
	}
	return b.String()
}

func clipSummary(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}

// verifyEnvAllowlist is the only set of parent variables forwarded to project
// test code. Secrets such as API keys and proxy credentials are intentionally
// omitted.
var verifyEnvAllowlist = []string{
	"PATH", "HOME", "USER", "LOGNAME", "SHELL", "LANG", "LC_ALL", "LC_CTYPE", "TZ",
	"TMPDIR", "TMP", "TEMP",
	"GOROOT", "GOPATH", "GOCACHE", "GOMODCACHE", "GOTOOLCHAIN",
	"GOOS", "GOARCH", "GOARM", "GOAMD64", "GOEXPERIMENT",
	"CGO_ENABLED", "CC", "CXX", "PKG_CONFIG",
	"PREFIX", "ANDROID_ROOT", "ANDROID_DATA", "ANDROID_STORAGE",
	"SSL_CERT_FILE", "SSL_CERT_DIR",
	"SYSTEMROOT", "WINDIR", "COMSPEC", "PATHEXT",
	"USERPROFILE", "HOMEDRIVE", "HOMEPATH", "APPDATA", "LOCALAPPDATA",
}

func buildVerifyEnv() []string {
	env := make([]string, 0, len(verifyEnvAllowlist)+4)
	for _, key := range verifyEnvAllowlist {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	env = append(env,
		"GOMEMLIMIT=512MiB",
		"GOGC=50",
		"GOFLAGS=-mod=readonly",
		"GOPROXY=off",
		"GOTOOLCHAIN=local",
	)
	if os.Getenv("TMPDIR") == "" {
		if prefix := os.Getenv("PREFIX"); prefix != "" {
			env = append(env, "TMPDIR="+filepath.Join(prefix, "tmp"))
		}
	}
	return env
}

func truncateOutput(s string) string {
	if len(s) <= maxOutputBytes {
		return s
	}
	half := maxOutputBytes / 2

	head := s[:half]
	for len(head) > 0 && !utf8.ValidString(head) {
		head = head[:len(head)-1]
	}

	tail := s[len(s)-half:]
	for len(tail) > 0 && !utf8.ValidString(tail) {
		tail = tail[1:]
	}

	return head + "\n...[output truncated]...\n" + tail
}
