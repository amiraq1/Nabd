package sandbox

import (
	"strings"
	"testing"
)

func TestParseMode(t *testing.T) {
	tests := []struct {
		raw  string
		want Mode
	}{
		{"", ModeAuto},
		{" auto ", ModeAuto},
		{"ON", ModeOn},
		{"off", ModeOff},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got, err := ParseMode(tt.raw)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("ParseMode(%q) = %d, want %d", tt.raw, got, tt.want)
			}
		})
	}
}

func TestParseModeRejectsUnknown(t *testing.T) {
	_, err := ParseMode("maybe")
	if err == nil || !strings.Contains(err.Error(), "auto, on, off") {
		t.Fatalf("ParseMode(maybe) error = %v, want allowed-values error", err)
	}
}

func TestUseHelperModes(t *testing.T) {
	tests := []struct {
		name     string
		mode     Mode
		helper   bool
		landlock bool
		want     bool
		wantErr  bool
	}{
		{"auto available", ModeAuto, true, true, true, false},
		{"auto fallback", ModeAuto, false, false, false, false},
		{"off overrides available", ModeOff, true, true, false, false},
		{"on available", ModeOn, true, true, true, false},
		{"on fails closed", ModeOn, true, false, false, true},
		{"on fails without helper", ModeOn, false, true, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := UseHelper(tt.mode, tt.helper, tt.landlock)
			if (err != nil) != tt.wantErr {
				t.Fatalf("UseHelper() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("UseHelper() = %v, want %v", got, tt.want)
			}
		})
	}
}
