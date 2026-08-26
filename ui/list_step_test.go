package ui

import (
	"testing"

	"claude-squad/session"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// listOf builds a List with n placeholder instances. StepSelection only moves an
// index, so the instances need no tmux session or worktree behind them.
func listOf(t *testing.T, n int) *List {
	t.Helper()
	l := &List{}
	for i := 0; i < n; i++ {
		l.items = append(l.items, &session.Instance{})
	}
	return l
}

func TestStepSelection(t *testing.T) {
	t.Run("forward and back", func(t *testing.T) {
		l := listOf(t, 3)

		l.StepSelection(1)
		assert.Equal(t, 1, l.selectedIdx)
		l.StepSelection(1)
		assert.Equal(t, 2, l.selectedIdx)
		l.StepSelection(-1)
		assert.Equal(t, 1, l.selectedIdx)
	})

	t.Run("wraps past the end rather than stopping", func(t *testing.T) {
		// Stopping at the last session would make the key silently do nothing,
		// which reads as a broken binding.
		l := listOf(t, 3)
		l.selectedIdx = 2

		l.StepSelection(1)

		assert.Equal(t, 0, l.selectedIdx)
	})

	t.Run("wraps backwards past the start", func(t *testing.T) {
		l := listOf(t, 3)

		l.StepSelection(-1)

		assert.Equal(t, 2, l.selectedIdx, "must not go negative and index out of range")
	})

	t.Run("a single session steps to itself", func(t *testing.T) {
		l := listOf(t, 1)

		l.StepSelection(1)
		assert.Equal(t, 0, l.selectedIdx)
		l.StepSelection(-1)
		assert.Equal(t, 0, l.selectedIdx)
	})

	t.Run("an empty list does not panic", func(t *testing.T) {
		l := listOf(t, 0)

		require.NotPanics(t, func() {
			l.StepSelection(1)
			l.StepSelection(-1)
		})
		assert.Equal(t, 0, l.selectedIdx)
	})

	t.Run("a full cycle returns to where it started", func(t *testing.T) {
		l := listOf(t, 4)

		for i := 0; i < 4; i++ {
			l.StepSelection(1)
		}

		assert.Equal(t, 0, l.selectedIdx)
	})
}
