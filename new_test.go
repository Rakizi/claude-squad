package main

import (
	"testing"

	"claude-squad/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cfg builds a config with profiles and a default. `def` is what
// default_program holds -- which is matched against a profile NAME, so passing
// a path here is the misconfiguration these tests exist to pin.
func cfg(def string, profiles ...config.Profile) *config.Config {
	return &config.Config{DefaultProgram: def, Profiles: profiles}
}

var (
	pClaude = config.Profile{Name: "claude", Program: "/home/x/.local/bin/claude"}
	pReview = config.Profile{Name: "review", Program: "/home/x/.local/bin/claude --permission-mode plan"}
)

func TestResolveProgram(t *testing.T) {
	t.Run("no flags: default_program resolves THROUGH the profile table", func(t *testing.T) {
		// ⛔ THE BUG THIS PINS. `new` read cfg.DefaultProgram directly, so with
		// profiles configured it would have run the literal string "claude"
		// rather than the profile's program -- losing every flag the profile
		// carries, silently, because a bare "claude" IS on PATH and starts.
		got, err := resolveProgram(cfg("claude", pClaude, pReview), "", "")

		require.NoError(t, err)
		assert.Equal(t, pClaude.Program, got)
	})

	t.Run("a default naming a FLAGGED profile brings its flags along", func(t *testing.T) {
		// The louder half of the same bug: reading the field raw would look for
		// a binary literally called "review".
		got, err := resolveProgram(cfg("review", pClaude, pReview), "", "")

		require.NoError(t, err)
		assert.Equal(t, pReview.Program, got)
		assert.Contains(t, got, "--permission-mode plan")
	})

	t.Run("a default matching no profile is still run literally", func(t *testing.T) {
		// This is upstream's behaviour for an install with no profiles, and it
		// must keep working -- that is the whole no-config path.
		got, err := resolveProgram(cfg("/usr/bin/aider"), "", "")

		require.NoError(t, err)
		assert.Equal(t, "/usr/bin/aider", got)
	})

	t.Run("--profile picks by name", func(t *testing.T) {
		got, err := resolveProgram(cfg("claude", pClaude, pReview), "", "review")

		require.NoError(t, err)
		assert.Equal(t, pReview.Program, got)
	})

	t.Run("--program is taken literally and does NOT consult profiles", func(t *testing.T) {
		got, err := resolveProgram(cfg("claude", pClaude), "review", "")

		require.NoError(t, err)
		assert.Equal(t, "review", got, "a literal command, even when a profile shares the name")
	})

	t.Run("both flags is REFUSED, not silently resolved one way", func(t *testing.T) {
		_, err := resolveProgram(cfg("claude", pClaude), "aider", "review")

		require.Error(t, err)
		assert.Equal(t, exitRefused, exitCodeFor(err))
	})

	t.Run("an unknown profile is refused AND names the real ones", func(t *testing.T) {
		// Refused, not could-not-look: the config was read successfully and it
		// says no such profile, so a different argument may well succeed.
		_, err := resolveProgram(cfg("claude", pClaude, pReview), "", "nope")

		require.Error(t, err)
		assert.Equal(t, exitRefused, exitCodeFor(err))
		assert.Contains(t, err.Error(), "claude")
		assert.Contains(t, err.Error(), "review",
			"listing the valid names is what makes the refusal actionable")
	})

	t.Run("an unknown profile with NO profiles configured says so", func(t *testing.T) {
		_, err := resolveProgram(cfg("/usr/bin/claude"), "", "review")

		require.Error(t, err)
		assert.Equal(t, exitRefused, exitCodeFor(err))
		assert.Contains(t, err.Error(), "no profiles are configured",
			"'no such profile' and 'no profiles at all' are different problems")
	})

	t.Run("empty config does not panic", func(t *testing.T) {
		got, err := resolveProgram(cfg(""), "", "")

		require.NoError(t, err)
		assert.Equal(t, "", got)
	})
}
