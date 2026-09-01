// Package tools: undo.go walks the edit log backwards. It is deliberately
// not a Tool: the model may write, but only the human may rewind. An agent
// that can undo its own work can also erase the evidence of it.
package tools

import (
	"errors"

	"nabd/internal/snap"
)

// UndoResult is one attempted rewind, in the order attempted.
type UndoResult struct {
	Rel  string
	Note string
	OK   bool
}

func (e *editLog) last() (Edit, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.l) == 0 {
		return Edit{}, false
	}
	return e.l[len(e.l)-1], true
}

// drop removes the newest entry. Called only after a restore succeeded, so a
// refused rewind leaves the log intact and the next /undo sees the same head.
func (e *editLog) drop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.l) > 0 {
		e.l = e.l[:len(e.l)-1]
	}
}

func (e *editLog) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.l)
}

// Undo rewinds up to n edits, newest first, and stops at the first refusal:
// a chain of rewinds that skips a link would leave the tree in a state no
// snapshot ever described.
func (r *Registry) Undo(n int) []UndoResult {
	var out []UndoResult
	for i := 0; i < n; i++ {
		e, ok := r.edits.last()
		if !ok {
			out = append(out, UndoResult{Note: "لا تعديلات للتراجع عنها"})
			break
		}
		res := r.rewind(e)
		out = append(out, res)
		if !res.OK {
			break
		}
		r.edits.drop()
	}
	return out
}

func (r *Registry) rewind(e Edit) UndoResult {
	abs, err := r.root.Resolve(e.Rel)
	if err != nil {
		return UndoResult{Rel: e.Rel, Note: err.Error()}
	}
	now, err := r.sh.Capture(abs)
	if err != nil {
		return UndoResult{Rel: e.Rel, Note: err.Error()}
	}
	// The file must still be exactly as the agent left it. If a human touched
	// it since, restoring would silently destroy their work, which is a worse
	// failure than refusing to undo at all.
	if !snap.Unchanged(now, e.After) {
		return UndoResult{Rel: e.Rel, Note: "تغيّر بعد كتابة الوكيل؛ لن أدهس عملك"}
	}
	if err := r.sh.Restore(e.Before); err != nil {
		return UndoResult{Rel: e.Rel, Note: err.Error()}
	}
	verb := "أُعيد"
	if e.Before.Absent {
		verb = "حُذف"
	}
	return UndoResult{Rel: e.Rel, OK: true, Note: verb + " (" + e.Tool + ")"}
}

// Pending lists what /undo would walk, newest first.
func (r *Registry) Pending() []Edit {
	all := r.edits.all()
	rev := make([]Edit, 0, len(all))
	for i := len(all) - 1; i >= 0; i-- {
		rev = append(rev, all[i])
	}
	return rev
}

var ErrNoEdits = errors.New("لا تعديلات")
