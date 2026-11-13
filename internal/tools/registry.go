package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

const maxOutputBytes = 4096

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
	r := &Registry{
		workDir:  absRoot,
		approve:  approve,
		handlers: make(map[string]ToolSpec),
	}
	r.registerDefaults()
	return r, nil
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

func (r *Registry) resolveSafePath(relPath string) (string, error) {
	if strings.TrimSpace(relPath) == "" {
		return "", fmt.Errorf("empty path is not allowed")
	}
	if strings.ContainsRune(relPath, 0) {
		return "", fmt.Errorf("invalid path: contains NUL byte")
	}
	cleaned := filepath.Clean(relPath)
	if filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("absolute paths are not allowed: %s", relPath)
	}

	target := filepath.Join(r.workDir, cleaned)
	rel, err := filepath.Rel(r.workDir, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path outside workspace not allowed: %s", relPath)
	}
	if isSensitivePath(rel) {
		return "", fmt.Errorf("access to sensitive path denied: %s", relPath)
	}

	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		relResolved, err := filepath.Rel(r.workDir, resolved)
		if err != nil || relResolved == ".." || strings.HasPrefix(relResolved, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("symlink points outside workspace: %s", relPath)
		}
		if isSensitivePath(relResolved) {
			return "", fmt.Errorf("access to sensitive path denied: %s", relPath)
		}
	}

	return target, nil
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
	safe, err := r.resolveSafePath(relPath)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(safe)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
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

			safePath, err := r.resolveSafePath(p.Path)
			if err != nil {
				return "", err
			}
			contentBytes, err := os.ReadFile(safePath)
			if err != nil {
				return "", err
			}

			hasCRLF := bytes.Contains(contentBytes, []byte("\r\n"))
			content := strings.ReplaceAll(string(contentBytes), "\r\n", "\n")
			oldNorm := strings.ReplaceAll(p.OldText, "\r\n", "\n")
			newNorm := strings.ReplaceAll(p.NewText, "\r\n", "\n")

			count := strings.Count(content, oldNorm)
			if count == 0 {
				return "", fmt.Errorf("old_text not found in %s", p.Path)
			}
			if count != 1 {
				return "", fmt.Errorf("old_text appears %d times; include more surrounding lines to make it unique", count)
			}

			updatedBytes := []byte(strings.Replace(content, oldNorm, newNorm, 1))

			if strings.HasSuffix(safePath, ".go") {
				formatted, err := format.Source(updatedBytes)
				if err != nil {
					return "", fmt.Errorf("edit rejected due to Go syntax error:\n%v", err)
				}
				updatedBytes = formatted
			} else if hasCRLF {
				updatedBytes = bytes.ReplaceAll(updatedBytes, []byte("\n"), []byte("\r\n"))
			}

			if err := os.WriteFile(safePath, updatedBytes, 0644); err != nil {
				return "", err
			}
			return fmt.Sprintf("Successfully updated %s", p.Path), nil
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
			_ = json.Unmarshal(args, &p)
			pkg := strings.TrimSpace(p.Pkg)
			if pkg == "" {
				pkg = "./..."
			}

			if strings.HasPrefix(pkg, "-") {
				return "", fmt.Errorf("invalid package pattern %q: flags are not allowed", pkg)
			}
			if pkg != "./..." && pkg != "." {
				if _, err := r.resolveSafePath(strings.TrimSuffix(pkg, "/...")); err != nil {
					return "", err
				}
			}

			tCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
			defer cancel()

			cmd := exec.CommandContext(tCtx, "go", "test", "-p", "2", "-vet=all", pkg)
			cmd.Dir = r.workDir
			cmd.Env = buildVerifyEnv()

			var out bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &out
			err := cmd.Run()
			return out.String(), err
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
			safe, err := r.resolveSafePath(p.Path)
			if err != nil {
				return "", err
			}
			data, err := os.ReadFile(safe)
			if err != nil {
				return "", err
			}
			return string(data), nil
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
			safe, err := r.resolveSafePath(p.Path)
			if err != nil {
				return "", err
			}
			entries, err := os.ReadDir(safe)
			if err != nil {
				return "", err
			}
			var b strings.Builder
			for _, e := range entries {
				if isSensitivePath(filepath.Join(p.Path, e.Name())) {
					continue
				}
				typ := "F"
				if e.IsDir() {
					typ = "D"
				}
				b.WriteString(fmt.Sprintf("%s  %s\n", typ, e.Name()))
			}
			return b.String(), nil
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
