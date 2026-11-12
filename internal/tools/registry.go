package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
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
	r := &Registry{
		workDir:  absRoot,
		approve:  approve,
		handlers: make(map[string]ToolSpec),
	}
	r.registerDefaults()
	return r, nil
}

func (r *Registry) resolveSafePath(relPath string) (string, error) {
	cleaned := filepath.Clean(relPath)
	target := filepath.Join(r.workDir, cleaned)
	rel, err := filepath.Rel(r.workDir, target)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("مسار غير مسموح خارج مساحة العمل: %s", relPath)
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
			return "تم رفض الإجراء من قبل المستخدم."
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
		Description:  "استبدال كتلة نص دقيقة في ملف. يفحص syntax لملفات Go قبل الحفظ.",
		NeedsConfirm: true,
		Parameters:   json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"old_text":{"type":"string"},"new_text":{"type":"string"}},"required":["path","old_text","new_text"]}`),
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			var p struct {
				Path    string `json:"path"`
				OldText string `json:"old_text"`
				NewText string `json:"new_text"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return "", err
			}
			safePath, err := r.resolveSafePath(p.Path)
			if err != nil {
				return "", err
			}
			contentBytes, err := os.ReadFile(safePath)
			if err != nil {
				return "", err
			}
			content := string(contentBytes)
			count := strings.Count(content, p.OldText)
			if count == 0 {
				return "", fmt.Errorf("old_text غير موجود في الملف %s", p.Path)
			}
			if count != 1 {
				return "", fmt.Errorf("old_text يتكرر %d مرات؛ أضف سياقاً أكثر لتحديده بدقة", count)
			}

			updated := strings.Replace(content, p.OldText, p.NewText, 1)

			if strings.HasSuffix(safePath, ".go") {
				fset := token.NewFileSet()
				if _, err := parser.ParseFile(fset, p.Path, updated, parser.AllErrors); err != nil {
					return "", fmt.Errorf("رفض التعديل بسبب خطأ syntax:\n%v", err)
				}
			}

			if err := os.WriteFile(safePath, []byte(updated), 0644); err != nil {
				return "", err
			}
			return fmt.Sprintf("تم تحديث %s بنجاح", p.Path), nil
		},
	}

	r.handlers["go_verify"] = ToolSpec{
		Name:         "go_verify",
		Description:  "تشغيل go test و go vet مع قيود ذاكرة مناسبة لـ Termux.",
		NeedsConfirm: false,
		Parameters:   json.RawMessage(`{"type":"object","properties":{"pkg":{"type":"string"}}}`),
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			var p struct {
				Pkg string `json:"pkg"`
			}
			_ = json.Unmarshal(args, &p)
			if p.Pkg == "" {
				p.Pkg = "./..."
			}

			tCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
			defer cancel()

			cmd := exec.CommandContext(tCtx, "go", "test", "-p", "2", "-vet=all", p.Pkg)
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
		Description:  "قراءة محتوى ملف",
		NeedsConfirm: false,
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			var p struct{ Path string `json:"path"` }
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
		Description:  "عرض محتويات مجلد",
		NeedsConfirm: false,
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			var p struct{ Path string `json:"path"` }
			_ = json.Unmarshal(args, &p)
			if p.Path == "" {
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
	return s[:half] + "\n...[تم تقليم المخرجات]...\n" + s[len(s)-half:]
}
