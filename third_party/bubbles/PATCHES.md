# Local deviations from upstream charmbracelet/bubbles

Upstream baseline: v1.0.0

## textarea: grapheme-cluster cursor navigation
- Files: textarea/textarea.go, textarea/textarea_test.go
- Why: CursorUp/CursorDown consumed uniseg-based CharOffset while advancing
  m.col with rw.RuneWidth per rune. The mismatch was masked while
  go-runewidth reported width 1 for nonspacing marks; it surfaced as cursor
  drift on Arabic tashkeel after the v0.0.29 bump (PR #69, reverted in #82).
- Change: navigation now iterates grapheme clusters and measures with uniseg.
- Upstream status: not yet reported.
- Re-sync: re-apply before accepting any upstream bubbles update.
