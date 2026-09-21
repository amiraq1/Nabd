package tools

import (
	"strings"
	"testing"
)

// This file holds the write-side structural guards. Like their read-side
// siblings in open_read_source_test.go, they parse the target files with
// go/parser and assert on the AST: calls are matched as calls, so comments and
// renames can neither fake nor break them, and every guard first proves its
// target file and target functions still exist so it cannot pass vacuously.
// The lexical-to-AST conversion is recorded in docs/TECH_DEBT.md
// (WRITE_COMMIT_SOURCE_SCRAPERS_FRAGILE).

// TestWriteCommitDelegatesToAdapters: T2d structural contract. The shared
// mutation tail (write_commit.go:commit) delegates to the platform adapters
// and never opens, stats, captures, or publishes a project file by path.
func TestWriteCommitDelegatesToAdapters(t *testing.T) {
	fset, f := parseToolSource(t, "write_commit.go")

	if !declaresFunc(f, "commit") {
		t.Fatal("write_commit.go no longer declares commit; the shared mutation tail moved and this guard would inspect nothing")
	}
	for _, want := range []string{"writePathFromRoot", "captureFromRoot", "writeFromRoot"} {
		if hits := identCalls(fset, f, want); len(hits) == 0 {
			t.Errorf("write_commit.go never calls %s; the mutation tail must delegate to the adapters", want)
		}
	}
	if hits := pkgCalls(fset, f, "sh", "Capture"); len(hits) > 0 {
		t.Errorf("write_commit.go calls sh.Capture at %s; mutations go through the adapters, not by path", strings.Join(hits, ", "))
	}
	if hits := pkgCalls(fset, f, "snap", "WriteAtomic"); len(hits) > 0 {
		t.Errorf("write_commit.go calls snap.WriteAtomic at %s; publish belongs to writeFromRoot", strings.Join(hits, ", "))
	}
	if hits := identCalls(fset, f, "mkdirParentDirs"); len(hits) > 0 {
		t.Errorf("write_commit.go calls mkdirParentDirs at %s; parent creation belongs to the adapters", strings.Join(hits, ", "))
	}
	for _, banned := range []struct{ pkg, sel string }{
		{"os", "Stat"},
		{"os", "ReadFile"},
	} {
		if hits := pkgCalls(fset, f, banned.pkg, banned.sel); len(hits) > 0 {
			t.Errorf("write_commit.go calls %s.%s at %s; the tail must not touch the filesystem by path",
				banned.pkg, banned.sel, strings.Join(hits, ", "))
		}
	}
}

// TestWriteCommitUnixAdapterIsDescriptorOnly: the unix adapters are
// descriptor-relative only. The relative path is the authority, the absolute
// path is reporting metadata, and no path-based shortcut may reappear.
func TestWriteCommitUnixAdapterIsDescriptorOnly(t *testing.T) {
	fset, f := parseToolSource(t, "write_commit_unix.go")

	for _, fn := range []string{"captureFromRoot", "writeFromRoot"} {
		if !declaresFunc(f, fn) {
			t.Fatalf("write_commit_unix.go no longer declares %s; the unix adapter moved and this guard would inspect nothing", fn)
		}
	}
	for _, want := range []struct{ pkg, sel string }{
		{"safefs", "OpenRead"},
		{"safefs", "WriteFileAtomic"},
	} {
		if hits := pkgCalls(fset, f, want.pkg, want.sel); len(hits) == 0 {
			t.Errorf("write_commit_unix.go never calls %s.%s; the unix adapter must stay descriptor-relative", want.pkg, want.sel)
		}
	}
	if hits := methodCalls(fset, f, "Resolve"); len(hits) > 0 {
		t.Errorf("write_commit_unix.go calls .Resolve at %s; the relative path is the authority here", strings.Join(hits, ", "))
	}
	for _, banned := range []struct{ pkg, sel string }{
		{"os", "Open"},
		{"os", "OpenFile"}, // the old lexical probe "os.Open" also matched OpenFile; keep that coverage explicitly
		{"os", "ReadFile"},
		{"snap", "WriteAtomic"},
	} {
		if hits := pkgCalls(fset, f, banned.pkg, banned.sel); len(hits) > 0 {
			t.Errorf("write_commit_unix.go calls %s.%s at %s; no path-based shortcut may reappear",
				banned.pkg, banned.sel, strings.Join(hits, ", "))
		}
	}
	if hits := identCalls(fset, f, "mkdirParentDirs"); len(hits) > 0 {
		t.Errorf("write_commit_unix.go calls mkdirParentDirs at %s; safefs owns parent creation here", strings.Join(hits, ", "))
	}
	if hits := methodCalls(fset, f, "EvalSymlinks"); len(hits) > 0 {
		t.Errorf("write_commit_unix.go calls EvalSymlinks at %s; resolution happens in the descriptor walk", strings.Join(hits, ", "))
	}
}

// TestWriteCommitOtherIsCompatibilityPath: the non-unix adapter is the
// documented compatibility path. Path-based access stays quarantined here and
// it must not reach into the safe-open API.
func TestWriteCommitOtherIsCompatibilityPath(t *testing.T) {
	fset, f := parseToolSource(t, "write_commit_other.go")

	for _, fn := range []string{"captureFromRoot", "writeFromRoot"} {
		if !declaresFunc(f, fn) {
			t.Fatalf("write_commit_other.go no longer declares %s; the compatibility adapter moved and this guard would inspect nothing", fn)
		}
	}
	if !hasBuildConstraint(f, "!unix") {
		t.Error("write_commit_other.go must carry the //go:build !unix constraint")
	}
	if !hasComment(f, "compatibility") {
		t.Error("write_commit_other.go must document itself as a compatibility path")
	}
	for _, want := range []struct{ pkg, sel string }{
		{"sh", "Capture"},
		{"snap", "WriteAtomic"},
	} {
		if hits := pkgCalls(fset, f, want.pkg, want.sel); len(hits) == 0 {
			t.Errorf("write_commit_other.go never calls %s.%s; the compatibility path must keep the documented behaviour", want.pkg, want.sel)
		}
	}
	if hits := identCalls(fset, f, "mkdirParentDirs"); len(hits) == 0 {
		t.Error("write_commit_other.go never calls mkdirParentDirs; the compatibility path must keep the documented behaviour")
	}
	for _, banned := range []struct{ pkg, sel string }{
		{"safefs", "OpenRead"},
		{"safefs", "WriteFileAtomic"},
	} {
		if hits := pkgCalls(fset, f, banned.pkg, banned.sel); len(hits) > 0 {
			t.Errorf("write_commit_other.go calls %s.%s at %s; it is the compatibility path",
				banned.pkg, banned.sel, strings.Join(hits, ", "))
		}
	}
}
