package presentation_test

import (
	"strings"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/presentation"
)

func TestFormatRouteNoticeVisibility(t *testing.T) {
	cases := []struct {
		name        string
		route       *agent.ProviderRoute
		wantVisible bool
		wantSubstr  string
	}{
		{
			name:        "nil route",
			route:       nil,
			wantVisible: false,
		},
		{
			name: "attempted status hidden",
			route: &agent.ProviderRoute{
				Provider: "groq",
				Model:    "llama-3",
				Attempt:  1,
				Status:   "attempted",
			},
			wantVisible: false,
		},
		{
			name: "exhausted status hidden",
			route: &agent.ProviderRoute{
				Provider: "groq",
				Model:    "llama-3",
				Attempt:  3,
				Status:   "exhausted",
				Reason:   "all routes failed",
			},
			wantVisible: false,
		},
		{
			name: "unknown status hidden",
			route: &agent.ProviderRoute{
				Provider: "groq",
				Model:    "llama-3",
				Attempt:  1,
				Status:   "unexpected_status",
				Reason:   "something",
			},
			wantVisible: false,
		},
		{
			name: "selected attempt 1 hidden (primary)",
			route: &agent.ProviderRoute{
				Provider: "anthropic",
				Model:    "claude-3-5-sonnet",
				Attempt:  1,
				Status:   "selected",
			},
			wantVisible: false,
		},
		{
			name: "selected attempt 0 hidden",
			route: &agent.ProviderRoute{
				Provider: "anthropic",
				Model:    "claude-3-5-sonnet",
				Attempt:  0,
				Status:   "selected",
			},
			wantVisible: false,
		},
		{
			name: "selected attempt negative hidden",
			route: &agent.ProviderRoute{
				Provider: "anthropic",
				Model:    "claude-3-5-sonnet",
				Attempt:  -1,
				Status:   "selected",
			},
			wantVisible: false,
		},
		{
			name: "selected attempt 2 visible (fallback)",
			route: &agent.ProviderRoute{
				Provider: "groq",
				Model:    "llama-3-70b",
				Attempt:  2,
				Status:   "selected",
				Reason:   "this should not appear",
			},
			wantVisible: true,
			wantSubstr:  "route selected: groq/llama-3-70b (attempt 2)",
		},
		{
			name: "failed attempt 1 visible",
			route: &agent.ProviderRoute{
				Provider: "anthropic",
				Model:    "claude-3-5-sonnet",
				Attempt:  1,
				Status:   "failed",
				Reason:   "HTTP 404: model not found",
			},
			wantVisible: true,
			wantSubstr:  "route failed: anthropic/claude-3-5-sonnet (attempt 1): HTTP 404: model not found",
		},
		{
			name: "failed attempt 0 visible (preserves signed number)",
			route: &agent.ProviderRoute{
				Provider: "anthropic",
				Model:    "claude-3-5-sonnet",
				Attempt:  0,
				Status:   "failed",
				Reason:   "bad attempt",
			},
			wantVisible: true,
			wantSubstr:  "route failed: anthropic/claude-3-5-sonnet (attempt 0): bad attempt",
		},
		{
			name: "failed attempt negative visible (preserves signed number)",
			route: &agent.ProviderRoute{
				Provider: "anthropic",
				Model:    "claude-3-5-sonnet",
				Attempt:  -1,
				Status:   "failed",
				Reason:   "negative attempt",
			},
			wantVisible: true,
			wantSubstr:  "route failed: anthropic/claude-3-5-sonnet (attempt -1): negative attempt",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, visible := presentation.FormatRouteNotice(tc.route)
			if visible != tc.wantVisible {
				t.Fatalf("visible = %v, want %v", visible, tc.wantVisible)
			}
			if tc.wantVisible {
				if got != tc.wantSubstr {
					t.Fatalf("got %q, want %q", got, tc.wantSubstr)
				}
			} else {
				if got != "" {
					t.Fatalf("got %q for hidden event, want empty string", got)
				}
			}
		})
	}
}

func TestFormatRouteNoticeSpecificHTTPStatuses(t *testing.T) {
	statuses := []struct {
		code   string
		reason string
	}{
		{"401", "HTTP 401: Unauthorized invalid api key"},
		{"403", "HTTP 403: Forbidden quota exceeded"},
		{"404", "HTTP 404: Not Found model deprecated"},
	}

	for _, s := range statuses {
		t.Run(s.code, func(t *testing.T) {
			r := &agent.ProviderRoute{
				Provider: "groq",
				Model:    "llama-3",
				Attempt:  1,
				Status:   "failed",
				Reason:   s.reason,
			}
			got, ok := presentation.FormatRouteNotice(r)
			if !ok {
				t.Fatal("expected failed route to be visible")
			}
			if !strings.Contains(got, s.reason) {
				t.Fatalf("notice %q missing expected reason %q", got, s.reason)
			}
		})
	}
}

func TestFormatRouteNoticePlaceholders(t *testing.T) {
	cases := []struct {
		name     string
		route    *agent.ProviderRoute
		wantText string
	}{
		{
			name: "blank provider",
			route: &agent.ProviderRoute{
				Provider: "   \t  ",
				Model:    "m",
				Attempt:  1,
				Status:   "failed",
				Reason:   "r",
			},
			wantText: "route failed: unknown-provider/m (attempt 1): r",
		},
		{
			name: "blank model",
			route: &agent.ProviderRoute{
				Provider: "p",
				Model:    "",
				Attempt:  1,
				Status:   "failed",
				Reason:   "r",
			},
			wantText: "route failed: p/unknown-model (attempt 1): r",
		},
		{
			name: "blank reason",
			route: &agent.ProviderRoute{
				Provider: "p",
				Model:    "m",
				Attempt:  1,
				Status:   "failed",
				Reason:   "   ",
			},
			wantText: "route failed: p/m (attempt 1): failed",
		},
		{
			name: "all fields blank",
			route: &agent.ProviderRoute{
				Provider: "",
				Model:    "",
				Attempt:  2,
				Status:   "selected",
			},
			wantText: "route selected: unknown-provider/unknown-model (attempt 2)",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := presentation.FormatRouteNotice(tc.route)
			if !ok {
				t.Fatal("expected visible notice")
			}
			if got != tc.wantText {
				t.Fatalf("got %q, want %q", got, tc.wantText)
			}
		})
	}
}

func TestFormatRouteNoticeSecurityMatrix(t *testing.T) {
	sentinels := []struct {
		name     string
		sentinel string
	}{
		{"Anthropic", "sk-ant-api03-abcdef1234567890"},
		{"OpenRouter", "sk-or-v1-abcdef1234567890"},
		{"Groq", "gsk_abcdef1234567890"},
		{"NVIDIA", "nvapi-abcdef1234567890"},
		{"GitHub", "ghp_12345678901234567890"},
		{"GitHubFineGrained", "github_pat_11AA0_exampleToken123456789"},
		{"Bearer", "Bearer mysecrettoken12345678"},
		{"AuthHeader", "Authorization: mysecrettoken12345678"},
	}

	fields := []string{"Provider", "Model", "Reason"}

	for _, f := range fields {
		for _, s := range sentinels {
			t.Run(f+"_"+s.name, func(t *testing.T) {
				r := &agent.ProviderRoute{
					StreamID: "secret-stream-id-12345",
					Provider: "prov",
					Model:    "mod",
					Attempt:  1,
					Status:   "failed",
					Reason:   "error detail",
				}
				switch f {
				case "Provider":
					r.Provider = "prov-" + s.sentinel
				case "Model":
					r.Model = "mod-" + s.sentinel
				case "Reason":
					r.Reason = "error: " + s.sentinel
				}

				got, ok := presentation.FormatRouteNotice(r)
				if !ok {
					t.Fatal("expected visible notice")
				}
				if strings.Contains(got, s.sentinel) {
					t.Fatalf("sentinel value leaked into notice for field %s", f)
				}
				if strings.Contains(got, "secret-stream-id-12345") {
					t.Fatal("StreamID leaked into notice")
				}
				if strings.Contains(got, "\n") || strings.Contains(got, "\r") {
					t.Fatal("notice contains newline")
				}
			})
		}
	}
}

func TestFormatRouteNoticeTerminalInjections(t *testing.T) {
	injections := []struct {
		name     string
		hostile  string
		disallow []string
	}{
		{"CSI_Color", "\x1b[31;1mRedText\x1b[0m", []string{"\x1b"}},
		{"OSC8_Hyperlink", "\x1b]8;;https://evil.example\x07Click\x1b]8;;\x07", []string{"\x1b", "evil.example"}},
		{"Newline_CRLF", "line1\r\nline2\nline3\rline4", []string{"\n", "\r"}},
		{"Controls", "bad\x00\x07\x08char", []string{"\x00", "\x07", "\x08"}},
		{"Bidi_RLO", "innocent\u202Ereversed", []string{"\u202E"}},
	}

	for _, inj := range injections {
		t.Run(inj.name, func(t *testing.T) {
			r := &agent.ProviderRoute{
				Provider: "p_" + inj.hostile,
				Model:    "m_" + inj.hostile,
				Attempt:  1,
				Status:   "failed",
				Reason:   "r_" + inj.hostile,
			}
			got, ok := presentation.FormatRouteNotice(r)
			if !ok {
				t.Fatal("expected visible notice")
			}
			for _, dis := range inj.disallow {
				if strings.Contains(got, dis) {
					t.Fatalf("hostile sequence %q survived in notice: %q", dis, got)
				}
			}
		})
	}
}

func TestFormatRouteNoticeImmutability(t *testing.T) {
	original := &agent.ProviderRoute{
		StreamID: "stream-123",
		Provider: "groq",
		Model:    "llama-3",
		Attempt:  2,
		Status:   "failed",
		Reason:   "HTTP 404",
	}

	snapshot := *original

	_, _ = presentation.FormatRouteNotice(original)

	if *original != snapshot {
		t.Fatalf("FormatRouteNotice mutated input struct: got %+v, want %+v", *original, snapshot)
	}
}
