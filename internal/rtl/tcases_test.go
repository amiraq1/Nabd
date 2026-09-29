// Code generated from the Spike 3.1 verified transcript
// ($TMPDIR/nabd-rtl-spike31/stage5_quad_output.txt); the T values there
// were cross-checked against FriBidi 1.0.16 and the Unicode-verified
// engine. DO NOT EDIT BY HAND.

package rtl

type tcase struct {
	name   string
	text   string
	dir    Direction
	levels []uint8
	visual string // empty when the case contains nonspacing marks (L3 is platform-specific)
}

var tcases = []tcase{
	{name: "T1 Auto", text: "a.go \u0645\u0647\u0645", dir: Auto, levels: []uint8{0, 0, 0, 0, 0, 1, 1, 1}, visual: "a.go \u0645\u0647\u0645"},
	{name: "T1 RTL", text: "a.go \u0645\u0647\u0645", dir: RTL, levels: []uint8{2, 2, 2, 2, 1, 1, 1, 1}, visual: "\u0645\u0647\u0645 a.go"},
	{name: "T1 LTR", text: "a.go \u0645\u0647\u0645", dir: LTR, levels: []uint8{0, 0, 0, 0, 0, 1, 1, 1}, visual: "a.go \u0645\u0647\u0645"},
	{name: "T2 Auto", text: "Result: \u0645\u0631\u062d\u0628\u0627 123 def", dir: Auto, levels: []uint8{0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 1, 1, 2, 2, 2, 0, 0, 0, 0}, visual: "Result: 123 \u0627\u0628\u062d\u0631\u0645 def"},
	{name: "T2 RTL", text: "Result: \u0645\u0631\u062d\u0628\u0627 123 def", dir: RTL, levels: []uint8{2, 2, 2, 2, 2, 2, 1, 1, 1, 1, 1, 1, 1, 1, 2, 2, 2, 1, 2, 2, 2}, visual: "def 123 \u0627\u0628\u062d\u0631\u0645 :Result"},
	{name: "T2 LTR", text: "Result: \u0645\u0631\u062d\u0628\u0627 123 def", dir: LTR, levels: []uint8{0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 1, 1, 2, 2, 2, 0, 0, 0, 0}, visual: "Result: 123 \u0627\u0628\u062d\u0631\u0645 def"},
	{name: "T3 Auto", text: "\u0645\u0631\u062d\u0628\u0627 internal/ui/feed.go", dir: Auto, levels: []uint8{1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2}, visual: "internal/ui/feed.go \u0627\u0628\u062d\u0631\u0645"},
	{name: "T3 RTL", text: "\u0645\u0631\u062d\u0628\u0627 internal/ui/feed.go", dir: RTL, levels: []uint8{1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2}, visual: "internal/ui/feed.go \u0627\u0628\u062d\u0631\u0645"},
	{name: "T3 LTR", text: "\u0645\u0631\u062d\u0628\u0627 internal/ui/feed.go", dir: LTR, levels: []uint8{1, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, visual: "\u0627\u0628\u062d\u0631\u0645 internal/ui/feed.go"},
	{name: "T4 Auto", text: "internal/ui/feed.go \u0645\u0631\u062d\u0628\u0627", dir: Auto, levels: []uint8{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 1}, visual: "internal/ui/feed.go \u0627\u0628\u062d\u0631\u0645"},
	{name: "T4 RTL", text: "internal/ui/feed.go \u0645\u0631\u062d\u0628\u0627", dir: RTL, levels: []uint8{2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 1, 1, 1, 1, 1, 1}, visual: "\u0627\u0628\u062d\u0631\u0645 internal/ui/feed.go"},
	{name: "T4 LTR", text: "internal/ui/feed.go \u0645\u0631\u062d\u0628\u0627", dir: LTR, levels: []uint8{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 1}, visual: "internal/ui/feed.go \u0627\u0628\u062d\u0631\u0645"},
	{name: "T5 Auto", text: "\u0661\u0662\u0663 abc \u0627\u0628\u062c 456", dir: Auto, levels: []uint8{2, 2, 2, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2}, visual: "\u0661\u0662\u0663 abc 456 \u062c\u0628\u0627"},
	{name: "T5 RTL", text: "\u0661\u0662\u0663 abc \u0627\u0628\u062c 456", dir: RTL, levels: []uint8{2, 2, 2, 1, 2, 2, 2, 1, 1, 1, 1, 1, 2, 2, 2}, visual: "456 \u062c\u0628\u0627 abc \u0661\u0662\u0663"},
	{name: "T5 LTR", text: "\u0661\u0662\u0663 abc \u0627\u0628\u062c 456", dir: LTR, levels: []uint8{2, 2, 2, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2}, visual: "\u0661\u0662\u0663 abc 456 \u062c\u0628\u0627"},
	{name: "T6 Auto", text: "(\u0645\u0631\u062d\u0628\u0627) [go]", dir: Auto, levels: []uint8{1, 1, 1, 1, 1, 1, 1, 1, 1, 2, 2, 1}, visual: "[go] (\u0627\u0628\u062d\u0631\u0645)"},
	{name: "T6 RTL", text: "(\u0645\u0631\u062d\u0628\u0627) [go]", dir: RTL, levels: []uint8{1, 1, 1, 1, 1, 1, 1, 1, 1, 2, 2, 1}, visual: "[go] (\u0627\u0628\u062d\u0631\u0645)"},
	{name: "T6 LTR", text: "(\u0645\u0631\u062d\u0628\u0627) [go]", dir: LTR, levels: []uint8{0, 1, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0}, visual: "(\u0627\u0628\u062d\u0631\u0645) [go]"},
	{name: "T7 Auto", text: "\u0627\u0641\u062a\u062d internal/ui/feed.go \u062b\u0645 \u0634\u063a\u0651\u0644 go test ./...", dir: Auto, levels: []uint8{1, 1, 1, 1, 1, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 1, 1, 1, 1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 2, 2, 2, 1, 1, 1, 1, 1, 1}, visual: ""},
	{name: "T7 RTL", text: "\u0627\u0641\u062a\u062d internal/ui/feed.go \u062b\u0645 \u0634\u063a\u0651\u0644 go test ./...", dir: RTL, levels: []uint8{1, 1, 1, 1, 1, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 1, 1, 1, 1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 2, 2, 2, 1, 1, 1, 1, 1, 1}, visual: ""},
	{name: "T7 LTR", text: "\u0627\u0641\u062a\u062d internal/ui/feed.go \u062b\u0645 \u0634\u063a\u0651\u0644 go test ./...", dir: LTR, levels: []uint8{1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, visual: ""},
	{name: "T8 Auto", text: "\u0645\u0631\u062d\u0628\u0627 hello (test)!", dir: Auto, levels: []uint8{1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 1}, visual: "!hello (test) \u0627\u0628\u062d\u0631\u0645"},
	{name: "T8 RTL", text: "\u0645\u0631\u062d\u0628\u0627 hello (test)!", dir: RTL, levels: []uint8{1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 1}, visual: "!hello (test) \u0627\u0628\u062d\u0631\u0645"},
	{name: "T8 LTR", text: "\u0645\u0631\u062d\u0628\u0627 hello (test)!", dir: LTR, levels: []uint8{1, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, visual: "\u0627\u0628\u062d\u0631\u0645 hello (test)!"},
	{name: "T9 Auto", text: "\u0645\u0631\u062d\u0628\u0627 (\u0633\u0644\u0627\u0645) [go] {x}", dir: Auto, levels: []uint8{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 2, 2, 1, 1, 1, 2, 1}, visual: "{x} [go] (\u0645\u0627\u0644\u0633) \u0627\u0628\u062d\u0631\u0645"},
	{name: "T9 RTL", text: "\u0645\u0631\u062d\u0628\u0627 (\u0633\u0644\u0627\u0645) [go] {x}", dir: RTL, levels: []uint8{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 2, 2, 1, 1, 1, 2, 1}, visual: "{x} [go] (\u0645\u0627\u0644\u0633) \u0627\u0628\u062d\u0631\u0645"},
	{name: "T9 LTR", text: "\u0645\u0631\u062d\u0628\u0627 (\u0633\u0644\u0627\u0645) [go] {x}", dir: LTR, levels: []uint8{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0}, visual: "(\u0645\u0627\u0644\u0633) \u0627\u0628\u062d\u0631\u0645 [go] {x}"},
	{name: "T10 Auto", text: "\u0645\u0631\u062d\u0628\u0627   ", dir: Auto, levels: []uint8{1, 1, 1, 1, 1, 1, 1, 1}, visual: "   \u0627\u0628\u062d\u0631\u0645"},
	{name: "T10 RTL", text: "\u0645\u0631\u062d\u0628\u0627   ", dir: RTL, levels: []uint8{1, 1, 1, 1, 1, 1, 1, 1}, visual: "   \u0627\u0628\u062d\u0631\u0645"},
	{name: "T10 LTR", text: "\u0645\u0631\u062d\u0628\u0627   ", dir: LTR, levels: []uint8{1, 1, 1, 1, 1, 0, 0, 0}, visual: "\u0627\u0628\u062d\u0631\u0645   "},
	{name: "T11a Auto", text: "a.go   ", dir: Auto, levels: []uint8{0, 0, 0, 0, 0, 0, 0}, visual: "a.go   "},
	{name: "T11a RTL", text: "a.go   ", dir: RTL, levels: []uint8{2, 2, 2, 2, 1, 1, 1}, visual: "   a.go"},
	{name: "T11a LTR", text: "a.go   ", dir: LTR, levels: []uint8{0, 0, 0, 0, 0, 0, 0}, visual: "a.go   "},
	{name: "T11b Auto", text: "   \u0645\u0631\u062d\u0628\u0627", dir: Auto, levels: []uint8{1, 1, 1, 1, 1, 1, 1, 1}, visual: "\u0627\u0628\u062d\u0631\u0645   "},
	{name: "T11b RTL", text: "   \u0645\u0631\u062d\u0628\u0627", dir: RTL, levels: []uint8{1, 1, 1, 1, 1, 1, 1, 1}, visual: "\u0627\u0628\u062d\u0631\u0645   "},
	{name: "T11b LTR", text: "   \u0645\u0631\u062d\u0628\u0627", dir: LTR, levels: []uint8{0, 0, 0, 1, 1, 1, 1, 1}, visual: "   \u0627\u0628\u062d\u0631\u0645"},
}
