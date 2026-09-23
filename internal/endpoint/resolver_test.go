package endpoint

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestTermuxResolvConfParsing(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "standard nameserver IPv4",
			input: "nameserver 8.8.8.8\nnameserver 8.8.4.4\n",
			want:  []string{"8.8.8.8:53", "8.8.4.4:53"},
		},
		{
			name:  "standard nameserver IPv6",
			input: "nameserver 2001:4860:4860::8888\nnameserver 2606:4700:4700::1111\n",
			want:  []string{"[2001:4860:4860::8888]:53", "[2606:4700:4700::1111]:53"},
		},
		{
			name: "mixed comments and whitespace",
			input: `
# This is a comment
; Another comment style
nameserver   1.1.1.1   
   nameserver 9.9.9.9

# nameserver 10.0.0.1
domain example.com
search mylan
options edns0
nameserver 8.8.8.8
`,
			want: []string{"1.1.1.1:53", "9.9.9.9:53", "8.8.8.8:53"},
		},
		{
			name:  "invalid IP addresses skipped",
			input: "nameserver not-an-ip\nnameserver 1.2.3.4\nnameserver 999.999.999.999\n",
			want:  []string{"1.2.3.4:53"},
		},
		{
			name:  "empty input",
			input: "",
			want:  nil,
		},
		{
			name:  "comments and blank lines only",
			input: "# line 1\n\n; line 2\n  # line 3\n",
			want:  nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseResolvConf(strings.NewReader(tc.input))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parseResolvConf() = %v, want %v", got, tc.want)
			}
		})
	}

	t.Run("nil reader", func(t *testing.T) {
		got := parseResolvConf(nil)
		if got != nil {
			t.Fatalf("expected nil for nil reader, got %v", got)
		}
	})
}

func TestTermuxResolvConfLoading(t *testing.T) {
	tmp := t.TempDir()
	etcDir := filepath.Join(tmp, "etc")
	if err := os.MkdirAll(etcDir, 0o755); err != nil {
		t.Fatal(err)
	}

	resolvPath := filepath.Join(etcDir, "resolv.conf")
	content := "nameserver 192.0.2.1\nnameserver 192.0.2.2\n"
	if err := os.WriteFile(resolvPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PREFIX", tmp)
	t.Setenv("NABD_PUBLIC_DNS", "")

	servers, err := loadTermuxNameservers()
	if err != nil {
		t.Fatalf("loadTermuxNameservers() failed: %v", err)
	}
	want := []string{"192.0.2.1:53", "192.0.2.2:53"}
	if !reflect.DeepEqual(servers, want) {
		t.Fatalf("servers = %v, want %v", servers, want)
	}
}

func TestTermuxResolvConfNoFallbackWithoutOptIn(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PREFIX", tmp) // no etc/resolv.conf exists here
	t.Setenv("NABD_PUBLIC_DNS", "")

	servers, err := loadTermuxNameservers()
	if err == nil {
		t.Fatalf("expected error without resolv.conf and without NABD_PUBLIC_DNS, got servers %v", servers)
	}
}

func TestTermuxResolvConfFallbackWithOptIn(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PREFIX", tmp) // no etc/resolv.conf exists here

	for _, optIn := range []string{"1", "true", "TRUE"} {
		t.Run("optIn_"+optIn, func(t *testing.T) {
			t.Setenv("NABD_PUBLIC_DNS", optIn)
			servers, err := loadTermuxNameservers()
			if err != nil {
				t.Fatalf("loadTermuxNameservers() failed with NABD_PUBLIC_DNS=%s: %v", optIn, err)
			}
			want := []string{"1.1.1.1:53", "8.8.8.8:53"}
			if !reflect.DeepEqual(servers, want) {
				t.Fatalf("servers = %v, want %v", servers, want)
			}
		})
	}
}
