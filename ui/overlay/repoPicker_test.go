package overlay

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
)

func TestRepoPicker(t *testing.T) {
	three := []string{"/lab/alpha", "/lab/beta", "/other/gamma"}

	t.Run("the first entry is selected, so confirming keeps today's behaviour", func(t *testing.T) {
		rp := NewRepoPicker(three)

		assert.Equal(t, "/lab/alpha", rp.GetSelectedRepo())
	})

	t.Run("HasMultiple gates the picker off for an unconfigured install", func(t *testing.T) {
		assert.False(t, NewRepoPicker(nil).HasMultiple())
		assert.False(t, NewRepoPicker([]string{"/lab/only"}).HasMultiple())
		assert.True(t, NewRepoPicker(three).HasMultiple())
	})

	t.Run("empty picker returns empty, not a panic", func(t *testing.T) {
		assert.Equal(t, "", NewRepoPicker(nil).GetSelectedRepo())
	})

	t.Run("down then up returns to where it started", func(t *testing.T) {
		rp := NewRepoPicker(three)

		assert.True(t, rp.HandleKeyPress(tea.KeyMsg{Type: tea.KeyDown}))
		assert.Equal(t, "/lab/beta", rp.GetSelectedRepo())
		assert.True(t, rp.HandleKeyPress(tea.KeyMsg{Type: tea.KeyUp}))
		assert.Equal(t, "/lab/alpha", rp.GetSelectedRepo())
	})

	t.Run("the cursor cannot run off either end", func(t *testing.T) {
		rp := NewRepoPicker(three)

		for i := 0; i < 10; i++ {
			rp.HandleKeyPress(tea.KeyMsg{Type: tea.KeyUp})
		}
		assert.Equal(t, "/lab/alpha", rp.GetSelectedRepo(), "must not run past the top")

		for i := 0; i < 10; i++ {
			rp.HandleKeyPress(tea.KeyMsg{Type: tea.KeyDown})
		}
		assert.Equal(t, "/other/gamma", rp.GetSelectedRepo(), "must not run past the bottom")
	})

	t.Run("j and k navigate too", func(t *testing.T) {
		rp := NewRepoPicker(three)

		assert.True(t, rp.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}}))
		assert.Equal(t, "/lab/beta", rp.GetSelectedRepo())
		assert.True(t, rp.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}}))
		assert.Equal(t, "/lab/alpha", rp.GetSelectedRepo())
	})

	t.Run("unrelated keys are NOT consumed", func(t *testing.T) {
		rp := NewRepoPicker(three)

		assert.False(t, rp.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}}),
			"consuming everything would swallow enter and escape")
		assert.False(t, rp.HandleKeyPress(tea.KeyMsg{Type: tea.KeyEnter}))
		assert.False(t, rp.HandleKeyPress(tea.KeyMsg{Type: tea.KeyEsc}))
	})

	t.Run("render shows every repo, and the parent so same-named repos differ", func(t *testing.T) {
		rp := NewRepoPicker([]string{"/lab/nag", "/elsewhere/nag"})
		rp.Focus()

		out := rp.Render()

		assert.Contains(t, out, "nag")
		assert.Contains(t, out, "/lab", "the parent must be shown to tell duplicates apart")
		assert.Contains(t, out, "/elsewhere")
	})
}
