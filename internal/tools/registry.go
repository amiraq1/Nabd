package tools

import (
	"bytes"
	"context"
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
)

const maxOutputBytes = 4096

type ApprovalFunc func(toolName, summary string) bool

type ToolSpec struct {
	Name         string
	Description  string
	Parameters   json.RawMessage
	NeedsConfirm bool
	Execute      func(ctx context.Context, args json.RawMessage) (string, error)
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

func (r *Registry) NeedsApproval(name string) bool {
	spec, ok := r.handlers[name]
	if !ok {
		return true
	}
	return spec.NeedsConfirm
}

func (r *Registry) resolveSafePath(relPath string) (string, error) {
	if strings.TrimSpace(relPath) == "" {
		return "", fmt.Errorf("empty path is not allowed")
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

	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		relResolved, err := filepath.Rel(r.workDir, resolved)
		if err != nil || relResolved == ".." || strings.HasPrefix(relResolved, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("symlink points outside workspace: %s", relPath)
		}
	}

	return target, nil
}

func (r *Registry) Run(ctx context.Context, name string, rawArgs json.RawMessage) string {
	spec, ok := r.handlers[name]
	if !ok {
		return fmt.Sprintf("Error: unknown tool %q", name)
	}
	if spec.NeedsConfirm && r.approve != nil {
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
		Name:         "edit_file",
		Description:  "Replace an exact block of text in a file. Validates and formats Go code before saving.",
		NeedsConfirm: true,
		Parameters:   json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Relative file path"},"old_text":{"type":"string","description":"Exact existing block to replace"},"new_text":{"type":"string","description":"Replacement block"}},"required":["path","old_text","new_text"]}`),
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
		Name:         "go_verify",
		Description:  "Run go test and go vet with Termux-safe memory limits.",
		NeedsConfirm: false,
		Parameters:   json.RawMessage(`{"type":"object","properties":{"pkg":{"type":"string","description":"Package pattern, default ./..."}}}`),
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
			env := append(os.Environ(),
				"GOMEMLIMIT=512MiB",
				"GOGC=50",
			)
			if os.Getenv("TMPDIR") == "" {
				if prefix := os.Getenv("PREFIX"); prefix != "" {
					env = append(env, "TMPDIR="+filepath.Join(prefix, "tmp"))
				}
			}
			cmd.Env = env

			var out bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &out
			err := cmd.Run()
			return out.String(), err
		},
	}

	r.handlers["read_file"] = ToolSpec{
		Name:         "read_file",
		Description:  "Read the contents of a file inside the workspace.",
		NeedsConfirm: false,
		Parameters:   json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Relative file path"}},"required":["path"]}`),
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
		Name:         "list_dir",
		Description:  "List directory contents inside the workspace.",
		NeedsConfirm: false,
		Parameters:   json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Relative directory path (default .)"}}}`),
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
				typ := "F"
				if e.IsDir() {
					typ = "D"
				}
				b.WriteString(fmt.Sprintf("%s  %s\n", typ, e.Name()))
			}
			return b.String(), nil
		},
	}
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
