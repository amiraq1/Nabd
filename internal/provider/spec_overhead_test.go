package provider

import (
	"testing"
)

// TestEncodeSpecOverheadDifference measures the fixed-overhead difference
// between Arabic and English tool specs for the SAME history — one of the
// D unknowns, solved without a network. The Arabic strings are the exact
// ones that shipped in commit 3c9926b; the English ones are current.
func TestEncodeSpecOverheadDifference(t *testing.T) {
	arSpecs := []ToolSpec{
		{Name: "read_file", Description: "اقرأ ملفًا نصيًا. الأسطر مرقّمة. استخدم offset وlimit للملفات الطويلة.", Schema: []byte(`{"type":"object","properties":{"path":{"type":"string","description":"مسار نسبي من جذر المشروع"},"offset":{"type":"integer","description":"أول سطر (يبدأ من ١)"}},"required":["path"]}`)},
		{Name: "write_file", Description: "يكتب ملفًا كاملًا داخل المشروع، وينشئ المجلدات الناقصة.", Schema: []byte(`{"type":"object","properties":{"path":{"type":"string","description":"مسار نسبي داخل المشروع"},"content":{"type":"string","description":"المحتوى الكامل الجديد"}},"required":["path","content"]}`)},
	}
	enSpecs := []ToolSpec{
		{Name: "read_file", Description: "Read a text file. Lines are numbered. Use offset and limit for long files.", Schema: []byte(`{"type":"object","properties":{"path":{"type":"string","description":"relative path from the project root"},"offset":{"type":"integer","description":"first line (starts at 1)"}},"required":["path"]}`)},
		{Name: "write_file", Description: "Write a whole file inside the project, creating missing directories.", Schema: []byte(`{"type":"object","properties":{"path":{"type":"string","description":"relative path inside the project"},"content":{"type":"string","description":"the full new content"}},"required":["path","content"]}`)},
	}

	// The SAME history for both runs: one user message, one read result.
	hist := []Message{
		{Role: User, Text: "اقرأ الملف"},
		{Role: User, ToolResults: []ToolResult{{ID: "r1", Output: "1|line one\n2|line two"}}},
	}

	o := &OpenAICompat{Model: "m"}
	arBytes, err := o.encode(Request{Messages: hist, Tools: arSpecs, MaxTok: 1024})
	if err != nil {
		t.Fatal(err)
	}
	enBytes, err := o.encode(Request{Messages: hist, Tools: enSpecs, MaxTok: 1024})
	if err != nil {
		t.Fatal(err)
	}

	diff := len(arBytes) - len(enBytes)
	t.Logf("same history: arabic-spec request=%d bytes, english-spec request=%d bytes, saved=%d bytes", len(arBytes), len(enBytes), diff)
	if diff <= 0 {
		t.Errorf("expected Arabic specs to cost more bytes, got arabic=%d english=%d", len(arBytes), len(enBytes))
	}
	// At the measured 2.41 bytes/token this is the token saving per request.
	t.Logf("at 2.41 bytes/token: ≈ %d tokens saved per request (fixed overhead difference)", int(float64(diff)/2.41))
}
