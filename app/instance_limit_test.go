package app

import (
	"strconv"
	"strings"
	"testing"

	"claude-squad/config"

	"github.com/stretchr/testify/assert"
)

func TestInstanceLimitReached(t *testing.T) {
	t.Run("instance_limit 3: the 3rd is allowed, the 4th is refused", func(t *testing.T) {
		cfg := &config.Config{InstanceLimit: 3}
		// `current` is how many already exist, so creating the Nth is checked
		// with current == N-1.
		assert.False(t, instanceLimitReached(cfg, 0), "the 1st create")
		assert.False(t, instanceLimitReached(cfg, 1), "the 2nd create")
		assert.False(t, instanceLimitReached(cfg, 2), "the 3rd create must be ALLOWED")
		assert.True(t, instanceLimitReached(cfg, 3), "the 4th create must be REFUSED")
		assert.True(t, instanceLimitReached(cfg, 4))
	})

	t.Run("a config with no instance_limit uses the default, not 0", func(t *testing.T) {
		// ⛔ The load-bearing negative. If the zero value were taken literally,
		// this install could not create a single session.
		cfg := &config.Config{}
		assert.False(t, instanceLimitReached(cfg, 0),
			"an unset limit must not refuse the very first session")
		assert.False(t, instanceLimitReached(cfg, config.DefaultInstanceLimit-1),
			"the last allowed session under the default")
		assert.True(t, instanceLimitReached(cfg, config.DefaultInstanceLimit),
			"one past the default must still be refused -- the cap has not been removed")
	})

	t.Run("a raised limit really is raised", func(t *testing.T) {
		// The point of the change: 10 was the old wall.
		cfg := &config.Config{InstanceLimit: 40}
		assert.False(t, instanceLimitReached(cfg, 10))
		assert.False(t, instanceLimitReached(cfg, 39))
		assert.True(t, instanceLimitReached(cfg, 40))
	})

	t.Run("the default constant is the config package's, not a second number", func(t *testing.T) {
		assert.Equal(t, config.DefaultInstanceLimit, GlobalInstanceLimit)
	})
}

func TestInstanceLimitErrorNamesTheNumberAndTheFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnvVar, dir)

	err := instanceLimitError(&config.Config{InstanceLimit: 3})
	msg := err.Error()

	assert.Contains(t, msg, " 3 ", "the error must name the limit actually in force")
	assert.Contains(t, msg, "instance_limit", "the error must name the key to change")
	assert.Contains(t, msg, config.ConfigPath(), "the error must name the file being read")
	assert.NotContains(t, msg, strconv.Itoa(config.DefaultInstanceLimit),
		"quoting the default instead of the configured value would mislead")
	assert.True(t, strings.Contains(msg, "can't create more than"),
		"the original wording is kept so existing muscle memory still reads")
}
