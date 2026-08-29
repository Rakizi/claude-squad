package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeRawConfig drops a literal config.json into an isolated config dir and
// points the loader at it. The JSON is written as TEXT on purpose: constructing
// a Config struct would silently supply every field, which is exactly the case
// these tests exist to rule out.
func writeRawConfig(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(ConfigDirEnvVar, dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ConfigFileName), []byte(body), 0644))
}

func TestInstanceLimitFromOldFormatConfig(t *testing.T) {
	// ⛔ THE COLLAPSED-STATE TRAP. A config.json written by any binary before
	// instance_limit existed has no such key, so json.Unmarshal leaves the field
	// at 0. Taken literally that is a limit of zero and the user can create
	// nothing. "Absent" and "zero" must not be the same answer.
	writeRawConfig(t, `{
  "default_program": "claude",
  "auto_yes": false,
  "daemon_poll_interval": 1000,
  "branch_prefix": "rakizi/"
}`)

	cfg := LoadConfig()

	require.Equal(t, 0, cfg.InstanceLimit,
		"precondition: the raw field must really be absent, or this test proves nothing")
	assert.Equal(t, DefaultInstanceLimit, cfg.GetInstanceLimit(),
		"an old config must resolve to the default, not to a limit of 0")
	assert.NotZero(t, cfg.GetInstanceLimit(),
		"a resolved limit of 0 locks the user out of creating any session")
}

func TestInstanceLimitFromConfigFile(t *testing.T) {
	t.Run("an explicit value is honoured", func(t *testing.T) {
		writeRawConfig(t, `{"default_program":"claude","instance_limit":3}`)
		cfg := LoadConfig()
		require.Equal(t, 3, cfg.InstanceLimit, "the key did not survive the round trip")
		assert.Equal(t, 3, cfg.GetInstanceLimit())
	})

	t.Run("an explicit value above the default is honoured", func(t *testing.T) {
		// Guards against a mutation that clamps to DefaultInstanceLimit.
		writeRawConfig(t, `{"default_program":"claude","instance_limit":50}`)
		assert.Equal(t, 50, LoadConfig().GetInstanceLimit())
	})

	t.Run("an explicit 0 means the default, not a cap of zero", func(t *testing.T) {
		// DECISION: 0 resolves to the default. It cannot mean "unlimited" --
		// unlimited would need a distinct sentinel, and nobody asked for one --
		// and it must not mean a literal zero, because that is indistinguishable
		// from the absent key above and would lock the user out.
		writeRawConfig(t, `{"default_program":"claude","instance_limit":0}`)
		assert.Equal(t, DefaultInstanceLimit, LoadConfig().GetInstanceLimit())
	})

	t.Run("a negative value means the default", func(t *testing.T) {
		writeRawConfig(t, `{"default_program":"claude","instance_limit":-5}`)
		assert.Equal(t, DefaultInstanceLimit, LoadConfig().GetInstanceLimit())
	})
}

func TestDefaultConfigCarriesTheLimit(t *testing.T) {
	// A fresh install must WRITE the key, so the number is visible and editable
	// rather than being an invisible fallback the user has to know about.
	assert.Equal(t, DefaultInstanceLimit, DefaultConfig().InstanceLimit)
	assert.Equal(t, 20, DefaultInstanceLimit, "the chosen default")
}

func TestGetInstanceLimitNilReceiver(t *testing.T) {
	var c *Config
	assert.Equal(t, DefaultInstanceLimit, c.GetInstanceLimit())
}

func TestConfigPathFollowsTheEnvironment(t *testing.T) {
	// The error message quotes this path. If it ignored CLAUDE_SQUAD_DIR it
	// would tell the reader to edit a file the program is not reading.
	dir := t.TempDir()
	t.Setenv(ConfigDirEnvVar, dir)
	assert.Equal(t, filepath.Join(dir, ConfigFileName), ConfigPath())
}
