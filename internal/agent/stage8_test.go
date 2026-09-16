package agent

import (
	"strings"
	"testing"
)

// TestCalibrationNoticeNeverReachesTheModel verifies that calibration, monitoring,
// and unclassified notices are filtered out from the model's wire projection,
// while world-changing notices (such as /undo results) pass through properly tagged.
func TestCalibrationNoticeNeverReachesTheModel(t *testing.T) {
	evs := []Event{
		{Seq: 1, Type: UserMsg, Text: "اقرأ README.md"},
		{
			Seq:            2,
			Parent:         1,
			Type:           Notice,
			NoticeCategory: NoticeCategoryCalibration,
			Calib:          &Calibration{PromptTokens: 500},
			Text:           "calibration: token ratio (observed prompt_tokens ÷ heuristic estimate) adopted 2.00 · conservative ratchet, rises only (measured prompt_tokens=500)",
		},
		{
			Seq:            3,
			Parent:         2,
			Type:           Notice,
			NoticeCategory: NoticeCategoryUndoResult,
			Text:           "/undo 1 - ok main.go - restored from shadow",
		},
		{
			Seq:            4,
			Parent:         3,
			Type:           Notice,
			NoticeCategory: NoticeCategoryUnknown, // Unclassified/unknown notice
			Text:           "unknown diagnostic notice: memory pressure normal",
		},
	}

	msgs := Messages(evs)

	// 1. Original user message remains a normal user message.
	if len(msgs) == 0 {
		t.Fatalf("expected messages, got empty slice")
	}
	if msgs[0].Role != "user" || msgs[0].Text != "اقرأ README.md" {
		t.Errorf("original user message corrupted: role=%q, text=%q", msgs[0].Role, msgs[0].Text)
	}

	// 2. Calibration notice produces NO provider.Message and the distinctive value is absent.
	// 3. Distinctive measurement value "2.00" is completely absent from all messages.
	for i, m := range msgs {
		if strings.Contains(m.Text, "2.00") {
			t.Errorf("msg[%d] leaked distinctive calibration value 2.00: %q", i, m.Text)
		}
		if strings.Contains(m.Text, "calibration") {
			t.Errorf("msg[%d] leaked calibration notice content: %q", i, m.Text)
		}
	}

	// 4. Unknown or unclassified notice does NOT reach the model.
	for i, m := range msgs {
		if strings.Contains(m.Text, "unknown diagnostic") {
			t.Errorf("msg[%d] leaked unknown/unclassified notice: %q", i, m.Text)
		}
	}

	// 5. The /undo notice produces exactly one message.
	var undoMsgs []string
	for _, m := range msgs {
		if strings.Contains(m.Text, "/undo 1") {
			undoMsgs = append(undoMsgs, m.Text)
		}
	}
	if len(undoMsgs) != 1 {
		t.Fatalf("expected exactly one /undo message, got %d: %v", len(undoMsgs), undoMsgs)
	}

	// 6. The /undo message carries the approved notice tag («notice»).
	expectedUndoText := "«notice» /undo 1 - ok main.go - restored from shadow"
	if undoMsgs[0] != expectedUndoText {
		t.Errorf("undo message improperly formatted: got %q, want %q", undoMsgs[0], expectedUndoText)
	}

	// 7. Exactly 2 messages in total: user message followed by /undo notice.
	if len(msgs) != 2 {
		t.Fatalf("expected exactly 2 messages (user, undo notice), got %d: %+v", len(msgs), msgs)
	}
	if msgs[1].Text != expectedUndoText {
		t.Errorf("second message ordering violated: got %q", msgs[1].Text)
	}
}

// TestAnswerOnTruncatedReadIsMarked verifies that when a turn relies on a truncated
// read that was not completed to EOF, a structured user-facing warning is produced
// with the actual path, lines read, and total lines, without polluting the model's
// message projection.
func TestAnswerOnTruncatedReadIsMarked(t *testing.T) {
	t.Run("CaseA_truncated_incomplete", func(t *testing.T) {
		// A truncated read with lines_read=120, total_lines=340 that is never completed.
		events := []Event{
			{Seq: 1, Type: UserMsg, Text: "اقرأ README.md"},
			{Seq: 2, Parent: 1, Type: ToolStart, Call: &ToolCall{ID: "c1", Name: "read_file"}},
			{Seq: 3, Parent: 2, Type: ToolEnd, Call: &ToolCall{ID: "c1", Name: "read_file", Output: "line 1...120", OK: true}},
			{
				Seq:    4,
				Parent: 3,
				Type:   EventRead,
				Read: &ReadRecord{
					Path:       "README.md",
					Truncated:  true,
					NextOffset: 121,
					LinesRead:  120,
					TotalLines: 340,
					Offset:     1,
				},
			},
			{Seq: 5, Parent: 4, Type: TurnStart},
			{Seq: 6, Parent: 5, Type: TextDelta, Text: "هذا ملخص الملف بناءً على ما قرأت."},
			{Seq: 7, Parent: 6, Type: TurnEnd},
		}

		incomplete := EvaluateTruncatedReads(events)
		if len(incomplete) != 1 {
			t.Fatalf("expected 1 incomplete read, got %d", len(incomplete))
		}
		inc := incomplete[0]
		if inc.Path != "README.md" {
			t.Errorf("expected path README.md, got %q", inc.Path)
		}
		if inc.LinesRead != 120 {
			t.Errorf("expected lines_read=120, got %d", inc.LinesRead)
		}
		if inc.TotalLines != 340 {
			t.Errorf("expected total_lines=340, got %d", inc.TotalLines)
		}

		warning := FormatTruncatedReadWarning(inc)
		if !strings.Contains(warning, "README.md") || !strings.Contains(warning, "120") || !strings.Contains(warning, "340") {
			t.Errorf("warning missing expected details: %q", warning)
		}

		// When emitted as a display notice, verify it does NOT enter model messages.
		displayEvs := append(events, Event{
			Seq:            8,
			Parent:         7,
			Type:           Notice,
			NoticeCategory: NoticeCategoryDisplay,
			Text:           warning,
		})
		msgs := Messages(displayEvs)
		for _, m := range msgs {
			if strings.Contains(m.Text, warning) || strings.Contains(m.Text, "partial read") {
				t.Errorf("warning leaked into model message context: %+v", m)
			}
			if strings.Contains(m.Text, "120 of 340") {
				t.Errorf("truncated read metrics leaked into model messages: %+v", m)
			}
		}
	})

	t.Run("CaseB_completed_from_start", func(t *testing.T) {
		// A complete read from the start (truncated=false).
		events := []Event{
			{Seq: 1, Type: UserMsg, Text: "اقرأ small.go"},
			{Seq: 2, Parent: 1, Type: ToolStart, Call: &ToolCall{ID: "c1", Name: "read_file"}},
			{Seq: 3, Parent: 2, Type: ToolEnd, Call: &ToolCall{ID: "c1", Name: "read_file", Output: "package main", OK: true}},
			{
				Seq:    4,
				Parent: 3,
				Type:   EventRead,
				Read: &ReadRecord{
					Path:       "small.go",
					Truncated:  false,
					LinesRead:  50,
					TotalLines: 50,
					Offset:     1,
				},
			},
			{Seq: 5, Parent: 4, Type: TurnStart},
			{Seq: 6, Parent: 5, Type: TextDelta, Text: "هذا هو الملف بالكامل."},
			{Seq: 7, Parent: 6, Type: TurnEnd},
		}

		incomplete := EvaluateTruncatedReads(events)
		if len(incomplete) != 0 {
			t.Fatalf("expected 0 incomplete reads for full read, got %d: %+v", len(incomplete), incomplete)
		}
	})

	t.Run("CaseC_truncated_then_completed", func(t *testing.T) {
		// Read 1 is cut at line 100 of 200.
		// Read 2 continues from line 101 and reads the remaining 100 lines to EOF.
		events := []Event{
			{Seq: 1, Type: UserMsg, Text: "اقرأ doc.txt"},
			{Seq: 2, Parent: 1, Type: ToolStart, Call: &ToolCall{ID: "c1", Name: "read_file"}},
			{Seq: 3, Parent: 2, Type: ToolEnd, Call: &ToolCall{ID: "c1", Name: "read_file", Output: "part 1", OK: true}},
			{
				Seq:    4,
				Parent: 3,
				Type:   EventRead,
				Read: &ReadRecord{
					Path:       "doc.txt",
					Truncated:  true,
					NextOffset: 101,
					LinesRead:  100,
					TotalLines: 200,
					Offset:     1,
				},
			},
			{Seq: 5, Parent: 4, Type: ToolStart, Call: &ToolCall{ID: "c2", Name: "read_file"}},
			{Seq: 6, Parent: 5, Type: ToolEnd, Call: &ToolCall{ID: "c2", Name: "read_file", Output: "part 2", OK: true}},
			{
				Seq:    7,
				Parent: 6,
				Type:   EventRead,
				Read: &ReadRecord{
					Path:       "doc.txt",
					Truncated:  false,
					NextOffset: 0,
					LinesRead:  100,
					TotalLines: 200,
					Offset:     101,
				},
			},
			{Seq: 8, Parent: 7, Type: TurnStart},
			{Seq: 9, Parent: 8, Type: TextDelta, Text: "الملف مكتمل."},
			{Seq: 10, Parent: 9, Type: TurnEnd},
		}

		incomplete := EvaluateTruncatedReads(events)
		if len(incomplete) != 0 {
			t.Fatalf("expected 0 incomplete reads after successful continuation, got %d: %+v", len(incomplete), incomplete)
		}
	})

	t.Run("CaseD_insufficient_continuation", func(t *testing.T) {
		// Subtest D1: Continuation also ends in truncated=true.
		t.Run("continuation_still_truncated", func(t *testing.T) {
			events := []Event{
				{
					Seq:  1,
					Type: EventRead,
					Read: &ReadRecord{
						Path:       "huge.txt",
						Truncated:  true,
						NextOffset: 101,
						LinesRead:  100,
						TotalLines: 500,
						Offset:     1,
					},
				},
				{
					Seq:  2,
					Type: EventRead,
					Read: &ReadRecord{
						Path:       "huge.txt",
						Truncated:  true,
						NextOffset: 201,
						LinesRead:  100,
						TotalLines: 500,
						Offset:     101,
					},
				},
			}

			incomplete := EvaluateTruncatedReads(events)
			if len(incomplete) != 1 {
				t.Fatalf("expected warning to remain for multi-step truncated read, got %d", len(incomplete))
			}
			if incomplete[0].Path != "huge.txt" || !incomplete[0].Truncated {
				t.Errorf("unexpected read status: %+v", incomplete[0])
			}
			if incomplete[0].TotalLines != 500 {
				t.Errorf("expected total lines 500, got %d", incomplete[0].TotalLines)
			}
		})

		// Subtest D2: Continuation leaves an unread gap.
		t.Run("continuation_leaves_gap", func(t *testing.T) {
			events := []Event{
				{
					Seq:  1,
					Type: EventRead,
					Read: &ReadRecord{
						Path:       "gap.txt",
						Truncated:  true,
						NextOffset: 101,
						LinesRead:  100,
						TotalLines: 500,
						Offset:     1,
					},
				},
				{
					Seq:  2,
					Type: EventRead,
					Read: &ReadRecord{
						Path:       "gap.txt",
						Truncated:  false, // reaches EOF of its own chunk
						LinesRead:  100,
						TotalLines: 500,
						Offset:     401, // gap between 101 and 400!
					},
				},
			}

			incomplete := EvaluateTruncatedReads(events)
			if len(incomplete) != 1 {
				t.Fatalf("expected warning to remain when gap is left unread, got %d", len(incomplete))
			}
			if incomplete[0].Path != "gap.txt" || !incomplete[0].Truncated {
				t.Errorf("expected gap.txt to remain marked as truncated, got %+v", incomplete[0])
			}
		})
	})
}
