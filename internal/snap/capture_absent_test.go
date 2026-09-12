package snap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// T2d: CaptureAbsent is a pure state constructor. The caller has already proved
// the target is gone (ENOENT from a descriptor-relative open), so this function
// builds the state and touches neither the target nor the shadow store.
func TestCaptureAbsentBuildsAbsentState(t *testing.T) {
	s := mk(t)
	abs := filepath.Join(s.root, "ghost.txt")

	st, err := s.CaptureAbsent(abs)
	if err != nil {
		t.Fatalf("CaptureAbsent: %v", err)
	}
	if !st.Absent {
		t.Error("Absent=false, want true")
	}
	if st.Blob != "" {
		t.Errorf("Blob=%q, want empty", st.Blob)
	}
	if st.Size != 0 {
		t.Errorf("Size=%d, want 0", st.Size)
	}
	if st.Mode != 0 {
		t.Errorf("Mode=%v, want 0", st.Mode)
	}
	if st.At.IsZero() {
		t.Error("At is zero, want a timestamp")
	} else if st.At.Location() != time.UTC {
		t.Errorf("At location=%v, want UTC", st.At.Location())
	}
	if st.Rel == "" {
		t.Error("Rel empty, want the path relative to the root")
	}
	if filepath.IsAbs(st.Rel) {
		t.Errorf("Rel=%q, want a relative path", st.Rel)
	}
}

// Rel must be built by the same rule as Capture and CaptureBytes: the path
// relative to the shadow root, slash-normalized.
func TestCaptureAbsentUsesCanonicalRelativePath(t *testing.T) {
	s := mk(t)

	// Capture on a missing path gives the canonical Rel for that path.
	missing := filepath.Join(s.root, "sub", "missing.txt")
	viaCapture, err := s.Capture(missing)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	viaAbsent, err := s.CaptureAbsent(missing)
	if err != nil {
		t.Fatalf("CaptureAbsent: %v", err)
	}
	if viaAbsent.Rel != viaCapture.Rel {
		t.Errorf("CaptureAbsent Rel=%q, Capture Rel=%q; must match", viaAbsent.Rel, viaCapture.Rel)
	}
	if viaAbsent.Absent != viaCapture.Absent {
		t.Errorf("CaptureAbsent Absent=%v, Capture Absent=%v; must match", viaAbsent.Absent, viaCapture.Absent)
	}

	// CaptureBytes gives the canonical Rel for an existing file at the same
	// path; CaptureAbsent must agree on Rel because Rel is independent of
	// presence.
	existing := filepath.Join(s.root, "sub", "present.txt")
	if err := os.MkdirAll(filepath.Dir(existing), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existing, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	viaBytes, err := s.CaptureBytes(existing, []byte("x\n"), 0o644)
	if err != nil {
		t.Fatalf("CaptureBytes: %v", err)
	}
	viaAbsentBytes, err := s.CaptureAbsent(existing)
	if err != nil {
		t.Fatalf("CaptureAbsent: %v", err)
	}
	if viaAbsentBytes.Rel != viaBytes.Rel {
		t.Errorf("CaptureAbsent Rel=%q, CaptureBytes Rel=%q; must match", viaAbsentBytes.Rel, viaBytes.Rel)
	}
}

// Absence has no content: no blob may be created, and in particular not the
// blob for empty content. The function must also not stat or read the target.
func TestCaptureAbsentCreatesNoBlob(t *testing.T) {
	s := mk(t)

	st, err := s.CaptureAbsent(filepath.Join(s.root, "phantom.txt"))
	if err != nil {
		t.Fatalf("CaptureAbsent: %v", err)
	}
	if st.Blob != "" {
		t.Errorf("Blob=%q, want empty: absence has no content", st.Blob)
	}

	for _, p := range regularFilesUnder(t, s.store) {
		t.Errorf("CaptureAbsent created a blob: %s", p)
	}

	requireCaptureAbsentIsPure(t)
}

// regularFilesUnder returns every regular file beneath dir. A missing dir has
// no files, which is the correct answer for a store nothing has written to.
func regularFilesUnder(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !d.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return out
}

// requireCaptureAbsentIsPure proves structurally that CaptureAbsent never
// inspects the target: its body must not call a path-based filesystem routine.
func requireCaptureAbsentIsPure(t *testing.T) {
	t.Helper()
	src, err := os.ReadFile("shadow.go")
	if err != nil {
		t.Fatalf("shadow.go: %v", err)
	}
	body := string(src)

	const sig = "func (s *Shadow) CaptureAbsent("
	start := strings.Index(body, sig)
	if start < 0 {
		t.Fatalf("shadow.go must define %s", sig)
	}
	rest := body[start:]
	end := strings.Index(rest, "\n}")
	if end < 0 {
		t.Fatalf("could not find the end of CaptureAbsent")
	}
	fn := rest[:end]
	for _, banned := range []string{"os.Lstat", "os.Stat", "os.ReadFile", "os.Open"} {
		if strings.Contains(fn, banned) {
			t.Errorf("CaptureAbsent must not call %s; it is a pure state constructor", banned)
		}
	}
}
