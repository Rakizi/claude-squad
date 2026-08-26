package app

import (
	"testing"

	"claude-squad/config"

	"github.com/stretchr/testify/assert"
)

func gateCfg(on bool, names ...string) *config.Config {
	c := &config.Config{ProfileOnNew: on}
	for _, n := range names {
		c.Profiles = append(c.Profiles, config.Profile{Name: n, Program: "claude --" + n})
	}
	return c
}

func TestShouldPickProfile(t *testing.T) {
	t.Run("DEFAULT OFF: n is byte-identical to what it always did", func(t *testing.T) {
		// ⛔ THE LOAD-BEARING CASE. An install that never sets profile_on_new
		// must see no change at all, no matter how many profiles it has. This is
		// what makes the change upstreamable rather than a behaviour break.
		assert.False(t, shouldPickProfile(gateCfg(false, "claude", "review"), false))
	})

	t.Run("on, with a real choice: the picker is offered", func(t *testing.T) {
		assert.True(t, shouldPickProfile(gateCfg(true, "claude", "review"), false))
	})

	t.Run("N is never touched, even with the option on", func(t *testing.T) {
		// N's prompt overlay ALREADY carries a profile picker. Two pickers for
		// one choice is worse than none, and this is the only thing stopping it.
		assert.False(t, shouldPickProfile(gateCfg(true, "claude", "review"), true))
	})

	t.Run("one profile is not a choice", func(t *testing.T) {
		assert.False(t, shouldPickProfile(gateCfg(true, "claude"), false),
			"a list of one is a keypress that teaches the reader nothing")
	})

	t.Run("no profiles configured", func(t *testing.T) {
		assert.False(t, shouldPickProfile(gateCfg(true), false))
	})

	t.Run("a nil config does not panic", func(t *testing.T) {
		assert.False(t, shouldPickProfile(nil, false))
		assert.False(t, shouldPickProfile(nil, true))
	})

	t.Run("three profiles still offered", func(t *testing.T) {
		// Guards against a mutation pinning the count at exactly two.
		assert.True(t, shouldPickProfile(gateCfg(true, "a", "b", "c"), false))
	})
}
