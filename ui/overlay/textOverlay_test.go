package overlay

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
)

func keyRune(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

// The must-hit case this issue is about: on origin/main, a stray key like 'x'
// dismisses AND runs the callback. Rakizi/the-lab#14.
func TestTextOverlay_GatingBlocksStrayKey_MustHit(t *testing.T) {
	fired := false
	ov := NewTextOverlay("content")
	ov.Gating = true
	ov.OnDismiss = func() { fired = true }

	closed := ov.HandleKeyPress(keyRune('x'))

	assert.False(t, closed, "a gating overlay must stay open on a key that is neither the action key nor esc/q")
	assert.False(t, fired, "a stray key must NOT run the gated action")
	assert.False(t, ov.Dismissed)
}

func TestTextOverlay_Gating(t *testing.T) {
	t.Run("enter proceeds and runs OnDismiss exactly once", func(t *testing.T) {
		calls := 0
		ov := NewTextOverlay("content")
		ov.Gating = true
		ov.OnDismiss = func() { calls++ }
		ov.OnCancel = func() { t.Fatal("OnCancel must not run on enter") }

		closed := ov.HandleKeyPress(tea.KeyMsg{Type: tea.KeyEnter})

		assert.True(t, closed)
		assert.True(t, ov.Dismissed)
		assert.Equal(t, 1, calls)
	})

	t.Run("the type's own action key proceeds like enter", func(t *testing.T) {
		calls := 0
		ov := NewTextOverlay("content")
		ov.Gating = true
		ov.ActionKey = "c"
		ov.OnDismiss = func() { calls++ }
		ov.OnCancel = func() { t.Fatal("OnCancel must not run on the action key") }

		closed := ov.HandleKeyPress(keyRune('c'))

		assert.True(t, closed)
		assert.Equal(t, 1, calls)
	})

	t.Run("esc cancels without running OnDismiss", func(t *testing.T) {
		dismissCalled := false
		cancelCalled := false
		ov := NewTextOverlay("content")
		ov.Gating = true
		ov.OnDismiss = func() { dismissCalled = true }
		ov.OnCancel = func() { cancelCalled = true }

		closed := ov.HandleKeyPress(tea.KeyMsg{Type: tea.KeyEsc})

		assert.True(t, closed)
		assert.True(t, ov.Dismissed)
		assert.False(t, dismissCalled, "esc must not run the gated action")
		assert.True(t, cancelCalled)
	})

	t.Run("q cancels without running OnDismiss", func(t *testing.T) {
		dismissCalled := false
		cancelCalled := false
		ov := NewTextOverlay("content")
		ov.Gating = true
		ov.OnDismiss = func() { dismissCalled = true }
		ov.OnCancel = func() { cancelCalled = true }

		closed := ov.HandleKeyPress(keyRune('q'))

		assert.True(t, closed)
		assert.False(t, dismissCalled)
		assert.True(t, cancelCalled)
	})

	t.Run("any other key is ignored and the overlay stays open", func(t *testing.T) {
		ov := NewTextOverlay("content")
		ov.Gating = true
		ov.OnDismiss = func() { t.Fatal("OnDismiss must not run") }
		ov.OnCancel = func() { t.Fatal("OnCancel must not run") }

		closed := ov.HandleKeyPress(keyRune('z'))

		assert.False(t, closed)
		assert.False(t, ov.Dismissed)
	})

	t.Run("a stray key does not consume the action key on a later press", func(t *testing.T) {
		calls := 0
		ov := NewTextOverlay("content")
		ov.Gating = true
		ov.ActionKey = "c"
		ov.OnDismiss = func() { calls++ }

		assert.False(t, ov.HandleKeyPress(keyRune('x')))
		assert.True(t, ov.HandleKeyPress(keyRune('c')))
		assert.Equal(t, 1, calls)
	})
}

func TestTextOverlay_NonGating(t *testing.T) {
	t.Run("any key closes and runs OnDismiss, matching the original behaviour", func(t *testing.T) {
		for _, msg := range []tea.KeyMsg{keyRune('x'), keyRune('q'), {Type: tea.KeyEsc}, {Type: tea.KeyEnter}} {
			calls := 0
			ov := NewTextOverlay("content")
			// Gating left at its zero value (false).
			ov.OnDismiss = func() { calls++ }

			closed := ov.HandleKeyPress(msg)

			assert.True(t, closed)
			assert.True(t, ov.Dismissed)
			assert.Equal(t, 1, calls)
		}
	})

	t.Run("a nil OnDismiss is safe to call through", func(t *testing.T) {
		ov := NewTextOverlay("content")
		assert.NotPanics(t, func() {
			closed := ov.HandleKeyPress(keyRune('x'))
			assert.True(t, closed)
		})
	})
}

func TestTextOverlay_Render_GatingFooter(t *testing.T) {
	t.Run("gating overlay shows a continue/cancel footer", func(t *testing.T) {
		ov := NewTextOverlay("body text")
		ov.Gating = true
		ov.SetWidth(40)

		out := ov.Render()

		assert.Contains(t, out, "enter to continue")
		assert.Contains(t, out, "esc to cancel")
	})

	t.Run("non-gating overlay has no footer", func(t *testing.T) {
		ov := NewTextOverlay("body text")
		ov.SetWidth(40)

		out := ov.Render()

		assert.NotContains(t, out, "to continue")
	})
}
