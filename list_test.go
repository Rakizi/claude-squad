package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"claude-squad/session"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatusName(t *testing.T) {
	// Every Status the session package defines must render as a word. A missing
	// case would otherwise ship as "unknown(3)" in output scripts parse.
	for status, want := range map[session.Status]string{
		session.Running: "running",
		session.Ready:   "ready",
		session.Loading: "loading",
		session.Paused:  "paused",
	} {
		assert.Equal(t, want, statusName(status))
	}

	t.Run("an unrecognised status says so rather than lying", func(t *testing.T) {
		assert.Equal(t, "unknown(99)", statusName(session.Status(99)))
	})
}

func TestInstanceViewJSON(t *testing.T) {
	t.Run("no sessions encodes as [] and not null", func(t *testing.T) {
		// A consumer must be able to range over the result without a nil check.
		var buf bytes.Buffer
		require.NoError(t, json.NewEncoder(&buf).Encode(make([]instanceView, 0)))

		assert.Equal(t, "[]\n", buf.String())
	})

	t.Run("the reported field names are the published contract", func(t *testing.T) {
		// These names are what scripts key off. Renaming one is a breaking
		// change, so it should fail here rather than silently in someone's jq.
		raw, err := json.Marshal(instanceView{Title: "t"})
		require.NoError(t, err)

		var got map[string]any
		require.NoError(t, json.Unmarshal(raw, &got))

		for _, field := range []string{
			"title", "repo", "branch", "status", "worktree",
			"tmux_session", "tmux_alive", "program", "created_at", "updated_at",
		} {
			assert.Contains(t, got, field)
		}
	})
}

func TestRenderInstanceTable(t *testing.T) {
	t.Run("a dead tmux session reads as gone, not as blank", func(t *testing.T) {
		// This is the condition that makes a session invisible in the interface
		// while it is still on disk. It must be legible at a glance.
		//
		// ⚠ Tmux is set EXPLICITLY here. The renderer reads that field, not
		// TmuxAlive, so an instanceView built by hand without it renders a blank
		// column -- which this test caught the moment the field was introduced.
		// loadInstanceViews is the only thing that should ever build one.
		var buf bytes.Buffer
		renderInstanceTable(&buf, []instanceView{
			{Title: "alive1", Status: "running", TmuxAlive: true, Tmux: "alive"},
			{Title: "dead1", Status: "running", TmuxAlive: false, Tmux: "gone"},
		})

		out := buf.String()
		assert.Contains(t, out, "alive")
		assert.Contains(t, out, "gone")
		assert.Contains(t, out, "TITLE")
	})

	t.Run("a paused session reads n/a, so it is not mistaken for damage", func(t *testing.T) {
		// A paused session HAS no tmux session by design. Rendering it the same
		// as a broken one invites someone to "fix" a working feature by
		// deleting it -- and kill deletes the branch, which is the only place a
		// paused session's work exists.
		var buf bytes.Buffer
		renderInstanceTable(&buf, []instanceView{
			{Title: "paused1", Status: "paused", TmuxAlive: false, Tmux: "n/a"},
		})

		out := buf.String()
		assert.Contains(t, out, "n/a")
		assert.NotContains(t, out, "gone", "paused is not gone")
	})
}

func TestTmuxState(t *testing.T) {
	t.Run("alive is alive whatever the status says", func(t *testing.T) {
		assert.Equal(t, "alive", tmuxState(session.Running, true))
		assert.Equal(t, "alive", tmuxState(session.Paused, true))
	})

	t.Run("PAUSED with no tmux is n/a, not gone", func(t *testing.T) {
		// ⛔ THE WHOLE POINT. `c` tears the tmux session down deliberately and
		// keeps the branch. Printing "gone" invites someone to treat a working
		// feature as damage — and then to "fix" it by deleting the instance.
		assert.Equal(t, "n/a", tmuxState(session.Paused, false))
	})

	t.Run("running or ready with no tmux IS gone — a real problem", func(t *testing.T) {
		// The discriminating pair: same absence, different meaning, and the
		// difference is entirely the status.
		assert.Equal(t, "gone", tmuxState(session.Running, false))
		assert.Equal(t, "gone", tmuxState(session.Ready, false))
		assert.Equal(t, "gone", tmuxState(session.Loading, false))
	})

	t.Run("the three values are distinct", func(t *testing.T) {
		// A helper returning one constant would pass every case above that
		// happens to expect it.
		got := map[string]bool{
			tmuxState(session.Running, true):  true,
			tmuxState(session.Paused, false):  true,
			tmuxState(session.Running, false): true,
		}
		assert.Len(t, got, 3, "alive, n/a and gone must not collapse")
	})
}
