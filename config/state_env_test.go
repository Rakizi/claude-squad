package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These exercise SaveState/LoadState themselves, not the path helpers. A helper
// returning the right string proves nothing if the save path does not call it --
// that gap is exactly how a config knob ends up documented and unwired.

func TestSaveStateHonoursTheEnvironment(t *testing.T) {
	t.Run("writes into CLAUDE_SQUAD_DIR", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv(ConfigDirEnvVar, dir)
		t.Setenv(StateFileEnvVar, "")

		s := DefaultState()
		require.NoError(t, s.SaveInstances(json.RawMessage(`[{"title":"probe"}]`)))

		written := filepath.Join(dir, StateFileName)
		require.FileExists(t, written, "SaveState did not use CLAUDE_SQUAD_DIR")

		b, err := os.ReadFile(written)
		require.NoError(t, err)
		assert.Contains(t, string(b), "probe")
	})

	t.Run("writes under CLAUDE_SQUAD_STATE_FILE", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv(ConfigDirEnvVar, dir)
		t.Setenv(StateFileEnvVar, "state-rakizi.json")

		s := DefaultState()
		require.NoError(t, s.SaveInstances(json.RawMessage(`[{"title":"named"}]`)))

		assert.FileExists(t, filepath.Join(dir, "state-rakizi.json"))
		assert.NoFileExists(t, filepath.Join(dir, StateFileName),
			"the default name must NOT also be written")
	})

	// The bug this whole change exists for.
	t.Run("TWO instances no longer clobber each other", func(t *testing.T) {
		dirA, dirB := t.TempDir(), t.TempDir()

		t.Setenv(ConfigDirEnvVar, dirA)
		t.Setenv(StateFileEnvVar, "")
		a := DefaultState()
		require.NoError(t, a.SaveInstances(json.RawMessage(`[{"title":"from-A"}]`)))

		t.Setenv(ConfigDirEnvVar, dirB)
		b := DefaultState()
		require.NoError(t, b.SaveInstances(json.RawMessage(`[{"title":"from-B"}]`)))

		// A must have SURVIVED B's save. Without the override both wrote the
		// same file and this is where A's sessions disappeared.
		t.Setenv(ConfigDirEnvVar, dirA)
		backA := LoadState()
		assert.Contains(t, string(backA.GetInstances()), "from-A")
		assert.NotContains(t, string(backA.GetInstances()), "from-B")

		t.Setenv(ConfigDirEnvVar, dirB)
		backB := LoadState()
		assert.Contains(t, string(backB.GetInstances()), "from-B")
		assert.NotContains(t, string(backB.GetInstances()), "from-A")
	})
}
