package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nabd/internal/perm"
)

// newGatedReg builds a registry wired exactly as cmd/ag wires it: one
// *perm.Policy is both the tool gate (via the registry's own classifier) and the
// path gate, with the session root's .gitignore loaded into it. A test writes
// that .gitignore to declare what the project excludes.
//
// Assertions ask the policy directly with CheckRead, and exercise the refusal
// end-to-end through read_file/grep, so both halves of the wiring are under
// test: SetIgnoreFile on the policy and SetPathGate on the registry.
func newGatedReg(t *testing.T, ignoreContent string) (*Registry, *perm.Policy, string) {
	t.Helper()
	r, dir := newReg(t)
	if ignoreContent != "" {
		if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(ignoreContent), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pol := perm.New(r) // the Registry is the Classifier, as in cmd/ag
	pol.SetIgnoreFile(dir)
	r.SetPathGate(pol)
	return r, pol, dir
}

func TestReadFileRefusesIgnoredPath(t *testing.T) {
	r, _, dir := newGatedReg(t, "secrets.env\n")
	secret := filepath.Join(dir, "secrets.env")
	if err := os.WriteFile(secret, []byte("token=whatever\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Control: the same bytes under a non-ignored name must read normally.
	if err := os.WriteFile(filepath.Join(dir, "open.txt"), []byte("visible\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	raw := json.RawMessage(`{"path":"secrets.env"}`)
	out, ok, err := r.Run(context.Background(), providerToolCall("read_file", raw))
	if ok || err == nil {
		t.Fatalf("read_file on an ignored path must be refused, got ok=%v err=%v out=%q", ok, err, out)
	}
	// The message must name the ignore rule — the refusal is a permission
	// decision about the path, not a containment error and not a stat miss.
	if !strings.Contains(err.Error(), "excluded by the session .gitignore") || !strings.Contains(err.Error(), `"secrets.env"`) {
		t.Fatalf("refusal must name the rule and the pattern, got: %v", err)
	}
	// A refusal must not leak bytes, whatever its wording.
	if strings.Contains(out+err.Error(), "token=whatever") {
		t.Fatalf("refused read leaked content: out=%q err=%v", out, err)
	}

	// Control read: the identical call against a non-ignored sibling succeeds,
	// so the refusal is attributable to the .gitignore line and nothing else.
	ctrl, ok, err := r.Run(context.Background(), providerToolCall("read_file", json.RawMessage(`{"path":"open.txt"}`)))
	if err != nil || !ok {
		t.Fatalf("control read_file failed: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(ctrl, "visible") {
		t.Fatalf("control read got %q", ctrl)
	}
}

// TestReadFileRefusalIsNotFromPathindex pins the acceptance requirement that the
// refusal happens on a direct path: no @ picker, no path index involved. The
// index is never built in this test (newReg does not run pathindex), so a pass
// here can only be explained by the permission layer.
func TestReadFileRefusalIsNotFromPathindex(t *testing.T) {
	r, pol, dir := newGatedReg(t, "/vendor/\n")
	if err := os.MkdirAll(filepath.Join(dir, "vendor", "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vendor", "lib", "x.so"), []byte("binary-ish\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	raw := json.RawMessage(`{"path":"vendor/lib/x.so"}`)
	_, ok, err := r.Run(context.Background(), providerToolCall("read_file", raw))
	if ok || err == nil {
		t.Fatalf("direct read into an excluded dir must be refused, got ok=%v err=%v", ok, err)
	}
	if !strings.Contains(err.Error(), "excluded by the session .gitignore") {
		t.Fatalf("refusal must come from the ignore rule, got: %v", err)
	}
	// And the same verdict comes from the policy on the same rel, quoted for the
	// trace: the tools layer is a pass-through, not a second opinion.
	if v, why := pol.CheckRead("vendor/lib/x.so"); v != perm.Deny || !strings.Contains(why, `"vendor/lib/x.so"`) {
		t.Fatalf("policy must independently refuse: v=%v why=%q", v, why)
	}
}

// TestReadFileWithoutGateUnchanged pins the pre-rule behaviour for any registry
// built without a path gate: an ignored path reads normally, so the feature
// cannot smuggle itself into sessions that did not wire it.
func TestReadFileWithoutGateUnchanged(t *testing.T) {
	r, dir := newReg(t)
	if err := os.WriteFile(filepath.Join(dir, "secrets.env"), []byte("token=whatever\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("secrets.env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, ok, err := r.Run(context.Background(), providerToolCall("read_file", json.RawMessage(`{"path":"secrets.env"}`)))
	if err != nil || !ok || !strings.Contains(out, "token=whatever") {
		t.Fatalf("ungated registry must read the file, got ok=%v err=%v out=%q", ok, err, out)
	}
}

// TestReadFileAllowReadsOverrideReadsIgnoredPath covers the declared override:
// the same policy in allow-reads mode lets the read through.
// TestGrepSkipsIgnoredFilesWithDisclosure covers the grep half: a file the rule
// excludes is not searched, and the summary says so instead of pretending the
// pattern found nothing.
func TestGrepSkipsIgnoredFilesWithDisclosure(t *testing.T) {
	r, _, dir := newGatedReg(t, "secret.txt\n")
	for name, content := range map[string]string{
		"secret.txt":  "needle here\n",
		"visible.txt": "no match\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	out, ok, err := r.Run(context.Background(), providerToolCall("grep", json.RawMessage(`{"pattern":"needle","path":"."}`)))
	if err != nil || !ok {
		t.Fatalf("grep failed: ok=%v err=%v", ok, err)
	}
	if strings.Contains(out, "needle here") {
		t.Fatalf("grep leaked an excluded file's content: %q", out)
	}
	if !strings.Contains(out, "1 files excluded by the session .gitignore") {
		t.Fatalf("grep must disclose the exclusion, got: %q", out)
	}
	// And the visible file is still searched normally.
	if !strings.Contains(out, "no match") {
		t.Fatalf("grep stopped searching visible files: %q", out)
	}
}

// TestGrepFromExcludedDirCannotEscape pins the `grep needle .` attack: when the
// search root is inside an excluded directory, every file under it carries the
// excluded prefix and is skipped — with the disclosure — rather than streamed
// out as match lines.
func TestGrepFromExcludedDirCannotEscape(t *testing.T) {
	r, _, dir := newGatedReg(t, "build/\n")
	if err := os.MkdirAll(filepath.Join(dir, "build"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "build", "out.txt"), []byte("needle in build\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, ok, err := r.Run(context.Background(), providerToolCall("grep", json.RawMessage(`{"pattern":"needle","path":"build"}`)))
	if err != nil || !ok {
		t.Fatalf("grep failed: ok=%v err=%v", ok, err)
	}
	if strings.Contains(out, "needle in build") {
		t.Fatalf("grep leaked excluded-dir content: %q", out)
	}
	if !strings.Contains(out, "1 files excluded by the session .gitignore") {
		t.Fatalf("grep must disclose the skip, got: %q", out)
	}
}

// TestGrepNoExclusionsNoDisclosure pins that the disclosure line appears only
// when something was actually skipped — a summary that cries wolf devalues the
// one that matters.
func TestGrepNoExclusionsNoDisclosure(t *testing.T) {
	r, _, dir := newGatedReg(t, "secret.txt\n")
	if err := os.WriteFile(filepath.Join(dir, "visible.txt"), []byte("needle here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, ok, err := r.Run(context.Background(), providerToolCall("grep", json.RawMessage(`{"pattern":"needle","path":"."}`)))
	if err != nil || !ok {
		t.Fatalf("grep failed: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(out, "needle here") {
		t.Fatalf("expected the visible match, got %q", out)
	}
	if strings.Contains(out, "excluded by the session .gitignore") {
		t.Fatalf("disclosure must not appear when nothing was skipped: %q", out)
	}
}
func TestReadFileAllowReadsOverrideReadsIgnoredPath(t *testing.T) {
	r, pol, dir := newGatedReg(t, "secrets.env\n")
	pol.SetMode(perm.ModeAllowReads)
	if err := os.WriteFile(filepath.Join(dir, "secrets.env"), []byte("token=whatever\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, ok, err := r.Run(context.Background(), providerToolCall("read_file", json.RawMessage(`{"path":"secrets.env"}`)))
	if err != nil || !ok || !strings.Contains(out, "token=whatever") {
		t.Fatalf("allow-reads must read the ignored file, got ok=%v err=%v out=%q", ok, err, out)
	}
}
