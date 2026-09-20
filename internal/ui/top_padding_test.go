package ui

import "testing"

// TestTopPaddingDoesNotDependOnFollow pins the invariant that the viewport top
// padding is a function of the rendered content length and the viewport height
// only. It must not depend on m.follow: previously it did, so pressing PgUp on
// short content (which clears follow) collapsed the padding to zero and the
// content jumped upward even though there was nothing to scroll.
func TestTopPaddingDoesNotDependOnFollow(t *testing.T) {
	m := NewFeed()
	m.width = 80
	m.height = 24
	m.lines = []string{"one", "two", "three"}
	lm := m.computeLayout()
	if len(m.lines) >= lm.ViewportRows {
		t.Fatalf("vacuous setup: content (%d lines) must be shorter than the viewport (%d rows)",
			len(m.lines), lm.ViewportRows)
	}

	m.follow = true
	following := m.viewportTopPadding(lm)
	m.follow = false
	notFollowing := m.viewportTopPadding(lm)

	if following != notFollowing {
		t.Fatalf("top padding depends on follow: follow=true -> %d, follow=false -> %d",
			following, notFollowing)
	}
}
