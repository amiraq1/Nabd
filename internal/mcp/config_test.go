package mcp

// Phase 0 ID -> test mapping (ADR-0003 P9):
//   MCP-SEC-01  TestMCPSec01EnvAllowlist      (helper only; spawn-time closure is Phase 2)
//   MCP-SEC-02  TestMCPSec02SecretRefs
//   MCP-SEC-03  TestMCPSec03RepoTreeRejected, TestMCPSec03SymlinkToRepoRejected,
//               TestLoadPinnedToDefaultPath, TestLoadConfigHomeAsRootRefused
//   MCP-SEC-16-20 TestMCPSec16_20SizeLimits
//   (extra)     TestMCPEnvReservedNames, TestMCPDuplicateKeysRejected,
//               TestMCPConfigValidation, TestMCPConfigRangeBoundaries,
//               TestMCPConfigDefaults, TestMCPConfigMissingFile

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("secure config loading refuses clearly on Windows")
	}
}

// writeConfig writes body to dir/name with 0600 and returns the path.
func writeConfig(t *testing.T, dir, name, body string) string {
	t.Helper()
	skipOnWindows(t)
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func validServerJSON() string {
	return `{"transport":"stdio","command":"/usr/bin/rea","args":["--mcp"],` +
		`"env":{"REA_X":"${REA_X}"},"tools":{"analyze_javascript_application":"read-only"}}`
}

func TestMCPSec03RepoTreeRejected(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"mcp.json", ".ag/mcp.json", "sub/dir/mcp.json"} {
		p := writeConfig(t, root, rel, `{"servers":{"rea":`+validServerJSON()+`}}`)
		if _, err := loadConfig(p, root); err == nil {
			t.Errorf("in-tree config %s was accepted", rel)
		} else if !strings.Contains(err.Error(), "project tree") {
			t.Errorf("in-tree config %s: wrong error: %v", rel, err)
		}
	}
	if _, err := loadConfig(root, root); err == nil {
		t.Error("project root as config path was accepted")
	}
}

func TestMCPSec03SymlinkToRepoRejected(t *testing.T) {
	root := t.TempDir()
	linkDir := t.TempDir()
	// ~/.ag itself symlinked into the project tree must not pass the check.
	if err := os.Symlink(root, filepath.Join(linkDir, "ag")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	p := writeConfig(t, filepath.Join(linkDir, "ag"), "mcp.json",
		`{"servers":{"rea":`+validServerJSON()+`}}`)
	if _, err := loadConfig(p, root); err == nil {
		t.Error("symlinked-into-repo config was accepted")
	} else if !strings.Contains(err.Error(), "project tree") {
		t.Errorf("wrong error: %v", err)
	}
}

// TestLoadPinnedToDefaultPath proves the public Load reads only DefaultPath():
// a valid config anywhere else is never picked up (fail-closed location pin).
// No HOME manipulation: the decoy id is unique, so its absence is conclusive
// regardless of what the real home directory contains.
func TestLoadPinnedToDefaultPath(t *testing.T) {
	skipOnWindows(t)
	root := t.TempDir()

	decoy := t.TempDir()
	writeConfig(t, decoy, "mcp.json", `{"servers":{"decoy-pinning-test":`+validServerJSON()+`}}`)
	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := cfg.Servers["decoy-pinning-test"]; ok {
		t.Fatal("Load picked up a config outside DefaultPath()")
	}
	wantSuffix := string(filepath.Separator) + ".ag" + string(filepath.Separator) + "mcp.json"
	if !strings.HasSuffix(DefaultPath(), wantSuffix) {
		t.Fatalf("DefaultPath() = %q, want suffix %q", DefaultPath(), wantSuffix)
	}
}

// TestLoadConfigHomeAsRootRefused documents the edge case: when the project
// root IS the home directory, ~/.ag/mcp.json always resolves inside the tree
// and is refused (fail-closed). The error must say so explicitly.
func TestLoadConfigHomeAsRootRefused(t *testing.T) {
	home := t.TempDir()
	p := writeConfig(t, filepath.Join(home, ".ag"), "mcp.json",
		`{"servers":{"rea":`+validServerJSON()+`}}`)
	_, err := loadConfig(p, home)
	if err == nil {
		t.Fatal("config was accepted with project root == home")
	}
	if !strings.Contains(err.Error(), "project tree") {
		t.Errorf("error should name the tree refusal, got: %v", err)
	}
	if !strings.Contains(err.Error(), "home directory itself") {
		t.Errorf("error should document the home-as-root edge case, got: %v", err)
	}
}

func TestMCPSec02SecretRefs(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	for _, entry := range []string{
		`"TOKEN": "plaintext-value"`,
		`"TOKEN": "prefix-${TOKEN}"`,
		`"TOKEN": ""`,
		`"TOKEN": "${}"`,
		`"TOKEN": "${9BAD}"`,
	} {
		body := `{"servers":{"rea":{"transport":"stdio","command":"/usr/bin/rea","env":{` + entry + `}}}}`
		p := writeConfig(t, home, "mcp.json", body)
		if _, err := loadConfig(p, root); err == nil {
			t.Errorf("explicit secret %s was accepted", entry)
		}
	}
	body := `{"servers":{"rea":{"transport":"stdio","command":"/usr/bin/rea","env":{"TOKEN": "${MY_TOKEN}"}}}}`
	p := writeConfig(t, home, "mcp.json", body)
	cfg, err := loadConfig(p, root)
	if err != nil {
		t.Fatalf("valid ${VAR} ref rejected: %v", err)
	}
	if got := cfg.Servers["rea"].Env["TOKEN"]; got != "${MY_TOKEN}" {
		t.Fatalf("env ref not preserved, got %q", got)
	}
}

func TestMCPEnvReservedNames(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	for _, name := range []string{"PATH", "HOME", "LD_PRELOAD", "LD_LIBRARY_PATH", "DYLD_LIBRARY_PATH", "DYLD_INSERT_LIBRARIES"} {
		body := `{"servers":{"rea":{"transport":"stdio","command":"/usr/bin/rea","env":{"` + name + `": "${` + name + `}"}}}}`
		if _, err := loadConfig(writeConfig(t, home, "mcp.json", body), root); err == nil {
			t.Errorf("reserved env name %q was accepted", name)
		}
	}
}

func TestMCPSec01EnvAllowlist(t *testing.T) {
	for _, n := range []string{"PATH", "TERM", "LANG", "LC_ALL", "LC_MESSAGES", "LC_CTYPE"} {
		if !isAllowedEnvName(n) {
			t.Errorf("allowlisted name %q denied", n)
		}
	}
	for _, n := range []string{"HOME", "AWS_SECRET_ACCESS_KEY", "OPENROUTER_API_KEY", "LD_PRELOAD", "PATH_EXTRA", "", "LC"} {
		if isAllowedEnvName(n) {
			t.Errorf("non-allowlisted name %q allowed", n)
		}
	}
}

func TestMCPSec16_20SizeLimits(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()

	big := `{"servers":{"rea":{"transport":"stdio","command":"/usr/bin/rea","args":["` +
		strings.Repeat("x", 300*1024) + `"]}}}`
	if _, err := loadConfig(writeConfig(t, home, "mcp.json", big), root); err == nil {
		t.Error("oversized file accepted")
	}

	var sb strings.Builder
	sb.WriteString(`{"servers":{`)
	for i := 0; i < MaxServers+1; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`"s` + itoa(i) + `":` + validServerJSON())
	}
	sb.WriteString(`}}`)
	if _, err := loadConfig(writeConfig(t, home, "mcp.json", sb.String()), root); err == nil {
		t.Error("too many servers accepted")
	}

	manyTools := `{"servers":{"rea":{"transport":"stdio","command":"/usr/bin/rea","tools":{`
	for i := 0; i < MaxToolsPerServer+1; i++ {
		if i > 0 {
			manyTools += ","
		}
		manyTools += `"tool` + itoa(i) + `":"read-only"`
	}
	manyTools += `}}}}`
	if _, err := loadConfig(writeConfig(t, home, "mcp.json", manyTools), root); err == nil {
		t.Error("too many tools accepted")
	}

	var envSb strings.Builder
	envSb.WriteString(`{"servers":{"rea":{"transport":"stdio","command":"/usr/bin/rea","env":{`)
	for i := 0; i < MaxEnvEntries+1; i++ {
		if i > 0 {
			envSb.WriteString(",")
		}
		envSb.WriteString(`"V` + itoa(i) + `":"${V` + itoa(i) + `}"`)
	}
	envSb.WriteString(`}}}}`)
	if _, err := loadConfig(writeConfig(t, home, "mcp.json", envSb.String()), root); err == nil {
		t.Error("too many env entries accepted")
	}

	var argSb strings.Builder
	argSb.WriteString(`{"servers":{"rea":{"transport":"stdio","command":"/usr/bin/rea","args":[`)
	for i := 0; i < MaxArgs+1; i++ {
		if i > 0 {
			argSb.WriteString(",")
		}
		argSb.WriteString(`"a` + itoa(i) + `"`)
	}
	argSb.WriteString(`]}}}`)
	if _, err := loadConfig(writeConfig(t, home, "mcp.json", argSb.String()), root); err == nil {
		t.Error("too many args accepted")
	}

	longName := strings.Repeat("n", MaxEnvNameLen+1)
	longNameBody := `{"servers":{"rea":{"transport":"stdio","command":"/usr/bin/rea","env":{"` + longName + `":"${X}"}}}}`
	if _, err := loadConfig(writeConfig(t, home, "mcp.json", longNameBody), root); err == nil {
		t.Error("oversized env name accepted")
	}

	longArg := `{"servers":{"rea":{"transport":"stdio","command":"/usr/bin/rea","args":["` +
		strings.Repeat("y", MaxArgLen+1) + `"]}}}`
	if _, err := loadConfig(writeConfig(t, home, "mcp.json", longArg), root); err == nil {
		t.Error("oversized arg accepted")
	}
}

func TestMCPDuplicateKeysRejected(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	bodies := []string{
		`{"servers":{"rea":{"transport":"stdio","command":"/usr/bin/rea","tools":{"t":"read-only","t":"execute"}}}}`,
		`{"servers":{"rea":{"transport":"stdio","transport":"sse","command":"/usr/bin/rea"}}}`,
		`{"servers":{"rea":{"transport":"stdio","command":"/usr/bin/rea"}},"servers":{}}`,
	}
	for _, body := range bodies {
		if _, err := loadConfig(writeConfig(t, home, "mcp.json", body), root); err == nil {
			t.Errorf("duplicate keys accepted: %s", body[:60])
		} else if !strings.Contains(err.Error(), "duplicate key") {
			t.Errorf("wrong error for duplicates: %v", err)
		}
	}
}

func TestMCPConfigValidation(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	// NOTE: range fields live INSIDE the server object; placing them beside
	// "rea" would fail at unmarshal time and never exercise the range checks.
	srv := func(extras string) string {
		return `{"servers":{"rea":{"transport":"stdio","command":"/usr/bin/rea"` + extras + `}}}`
	}
	cases := map[string]string{
		"unknown field":       `{"servers":{"rea":` + validServerJSON() + `},"bogus":1}`,
		"trailing content":    `{"servers":{}} garbage`,
		"bad server id":       `{"servers":{"Bad ID!":` + validServerJSON() + `}}`,
		"non-stdio transport": `{"servers":{"rea":{"transport":"sse","command":"/usr/bin/rea"}}}`,
		"relative command":    `{"servers":{"rea":{"transport":"stdio","command":"rea"}}}`,
		"bad tool name":       `{"servers":{"rea":{"transport":"stdio","command":"/usr/bin/rea","tools":{"Bad Name!":"read-only"}}}}`,
		"bad classification":  `{"servers":{"rea":{"transport":"stdio","command":"/usr/bin/rea","tools":{"t":"sometimes"}}}}`,
		"parallel -1":         srv(`,"max_parallel_calls":-1`),
		"parallel 17":         srv(`,"max_parallel_calls":17`),
		"timeout -5":          srv(`,"call_timeout_ms":-5`),
		"timeout 600001":      srv(`,"call_timeout_ms":600001`),
	}
	for name, body := range cases {
		if _, err := loadConfig(writeConfig(t, home, "mcp.json", body), root); err == nil {
			t.Errorf("%s: invalid config accepted", name)
		}
	}
	// Range violations name their bounds.
	for _, tc := range []struct{ body, want string }{
		{srv(`,"max_parallel_calls":-1`), "out of range [1,16]"},
		{srv(`,"max_parallel_calls":17`), "out of range [1,16]"},
		{srv(`,"call_timeout_ms":-5`), "out of range [0,600000]"},
		{srv(`,"call_timeout_ms":600001`), "out of range [0,600000]"},
	} {
		_, err := loadConfig(writeConfig(t, home, "mcp.json", tc.body), root)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("want error containing %q, got %v", tc.want, err)
		}
	}
}

func TestMCPConfigRangeBoundaries(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	srv := func(extras string) string {
		return `{"servers":{"rea":{"transport":"stdio","command":"/usr/bin/rea"` + extras + `}}}`
	}
	for _, extras := range []string{
		`,"max_parallel_calls":1`,
		`,"max_parallel_calls":16`,
		`,"call_timeout_ms":600000`,
	} {
		if _, err := loadConfig(writeConfig(t, home, "mcp.json", srv(extras)), root); err != nil {
			t.Errorf("boundary %s rejected: %v", extras, err)
		}
	}
	// 0 is the intentional "use default" sentinel: accepted, defaults applied.
	cfg, err := loadConfig(writeConfig(t, home, "mcp.json",
		srv(`,"max_parallel_calls":0,"call_timeout_ms":0`)), root)
	if err != nil {
		t.Fatalf("sentinel 0 rejected: %v", err)
	}
	got := cfg.Servers["rea"]
	if got.MaxParallelCalls != DefaultMaxParallelCalls || got.CallTimeoutMs != DefaultCallTimeoutMs {
		t.Errorf("sentinel 0: got %d/%d, want defaults %d/%d",
			got.MaxParallelCalls, got.CallTimeoutMs, DefaultMaxParallelCalls, DefaultCallTimeoutMs)
	}
}

func TestMCPConfigDefaults(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	p := writeConfig(t, filepath.Join(home, ".ag"), "mcp.json",
		`{"servers":{"rea":{"transport":"stdio","command":"/usr/bin/rea"}}}`)
	cfg, err := loadConfig(p, root)
	if err != nil {
		t.Fatalf("minimal valid config rejected: %v", err)
	}
	srv := cfg.Servers["rea"]
	if srv.Enabled {
		t.Error("Enabled defaults to true, want false (disabled by default)")
	}
	if srv.MaxParallelCalls != DefaultMaxParallelCalls {
		t.Errorf("MaxParallelCalls = %d, want %d", srv.MaxParallelCalls, DefaultMaxParallelCalls)
	}
	if srv.CallTimeoutMs != DefaultCallTimeoutMs {
		t.Errorf("CallTimeoutMs = %d, want %d", srv.CallTimeoutMs, DefaultCallTimeoutMs)
	}
}

func TestMCPConfigMissingFile(t *testing.T) {
	skipOnWindows(t)
	root := t.TempDir()
	missing := filepath.Join(t.TempDir(), "nope.json")
	cfg, err := loadConfig(missing, root)
	if err != nil {
		t.Fatalf("missing file should mean not configured, got error: %v", err)
	}
	if len(cfg.Servers) != 0 {
		t.Fatalf("expected no servers, got %d", len(cfg.Servers))
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [8]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}
