package tmux

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProgramName(t *testing.T) {
	// The program string is a shell command. It may be a bare name, an absolute
	// path, and may carry arguments -- the -p flag documents exactly that. All
	// three forms must identify the same agent.
	for _, tc := range []struct {
		program string
		want    string
		why     string
	}{
		{"claude", "claude", "bare name"},
		{"/home/rakizi/.local/bin/claude", "claude",
			"absolute path -- this form failed the old exact match, silently losing prompt detection"},
		{"claude --add-dir /home/rakizi/the-lab/lab-plugins", "claude",
			"arguments -- this form failed the old HasSuffix, losing the trust-prompt auto-answer"},
		{"/home/rakizi/.local/bin/claude --add-dir /x --permission-mode plan", "claude",
			"path AND arguments, which is what a profile actually looks like"},
		{"aider --model ollama_chat/gemma3:1b", "aider", "the example from the -p help text"},
		{"gemini", "gemini", ""},
		{"  claude  ", "claude", "surrounding whitespace"},
		{"", "", "empty is empty, not a panic"},
		{"   ", "", "whitespace only"},
	} {
		assert.Equal(t, tc.want, programName(tc.program), tc.why)
	}

	t.Run("a different program is NOT mistaken for claude", func(t *testing.T) {
		// The old HasSuffix would have accepted any path ending in the letters
		// "claude", and any program whose last argument did.
		assert.NotEqual(t, ProgramClaude, programName("bash --rcfile /opt/claude"))
		assert.NotEqual(t, ProgramClaude, programName("myclaude"),
			"a different binary that merely ends in the same letters")
	})
}
