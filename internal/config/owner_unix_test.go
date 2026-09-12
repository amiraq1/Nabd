//go:build !windows
// +build !windows

package config

import (
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

type mockStatFileInfo struct {
	name string
	uid  uint32
}

func (m mockStatFileInfo) Name() string       { return m.name }
func (m mockStatFileInfo) Size() int64        { return 100 }
func (m mockStatFileInfo) Mode() os.FileMode  { return 0o600 }
func (m mockStatFileInfo) ModTime() time.Time { return time.Now() }
func (m mockStatFileInfo) IsDir() bool        { return false }
func (m mockStatFileInfo) Sys() any           { return &syscall.Stat_t{Uid: m.uid} }

type nonStatFileInfo struct {
	mockStatFileInfo
}

func (m nonStatFileInfo) Sys() any { return nil }

func TestCheckOwnerPlatform(t *testing.T) {
	currentUID := uint32(os.Getuid())

	// Correct owner: matches current UID -> accepted
	okFI := mockStatFileInfo{name: "config.v2.json", uid: currentUID}
	if err := checkOwnerPlatform("/path/to/config.v2.json", okFI); err != nil {
		t.Fatalf("matching UID rejected: %v", err)
	}

	// Wrong owner: different UID -> refused with ownership error
	wrongUID := currentUID + 101
	badFI := mockStatFileInfo{name: "config.v2.json", uid: wrongUID}
	err := checkOwnerPlatform("/path/to/config.v2.json", badFI)
	if err == nil {
		t.Fatal("mismatched UID accepted; want refusal")
	}
	if !strings.Contains(err.Error(), "يملكه uid") {
		t.Fatalf("err=%v, want Arabic ownership refusal message", err)
	}

	// Sys() not returning *syscall.Stat_t -> skipped (nil)
	nonUnixFI := nonStatFileInfo{mockStatFileInfo{name: "config.v2.json", uid: currentUID}}
	if err := checkOwnerPlatform("/path/to/config.v2.json", nonUnixFI); err != nil {
		t.Fatalf("non-stat sys rejected: %v", err)
	}
}
