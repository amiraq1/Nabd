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

func TestParseNetworkMode(t *testing.T) {
	tests := []struct {
		raw  string
		want NetworkMode
	}{
		{"", NetworkAllow},
		{" allow ", NetworkAllow},
		{"DENY", NetworkDeny},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got, err := ParseNetworkMode(tt.raw)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("ParseNetworkMode(%q) = %d, want %d", tt.raw, got, tt.want)
			}
		})
	}
}

func TestParseNetworkModeRejectsUnknown(t *testing.T) {
	_, err := ParseNetworkMode("maybe")
	if err == nil || !strings.Contains(err.Error(), "allow, deny") {
		t.Fatalf("ParseNetworkMode(maybe) error = %v, want allowed-values error", err)
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

func TestSelectNetworkDenyFailsClosed(t *testing.T) {
	tests := []struct {
		name    string
		mode    Mode
		helper  bool
		fs      bool
		network bool
		want    bool
		wantErr bool
	}{
		{"all capabilities", ModeAuto, true, true, true, true, false},
		{"off cannot deny", ModeOff, true, true, true, false, true},
		{"no network ABI", ModeAuto, true, true, false, false, true},
		{"no helper", ModeAuto, false, true, true, false, true},
		{"no filesystem ABI", ModeAuto, true, false, true, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Select(tt.mode, NetworkDeny, tt.helper, tt.fs, tt.network)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Select() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("Select() = %v, want %v", got, tt.want)
			}
		})
	}
}
