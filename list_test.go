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
		var buf bytes.Buffer
		renderInstanceTable(&buf, []instanceView{
			{Title: "alive1", Status: "running", TmuxAlive: true},
			{Title: "dead1", Status: "running", TmuxAlive: false},
		})

		out := buf.String()
		assert.Contains(t, out, "alive")
		assert.Contains(t, out, "gone")
		assert.Contains(t, out, "TITLE")
	})
}
