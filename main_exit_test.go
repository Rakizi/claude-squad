package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFailureExitsNonZero builds the binary and runs it, because the thing under
// test is main()'s exit status and nothing short of a process can observe that.
//
// ⛔ A unit test asserting rootCmd.SilenceErrors is set would pass whether or not
// os.Exit(1) is reached. The defect was that a failure exited 0; only an exit
// code proves it does not.
func TestFailureExitsNonZero(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "cs-test")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Skipf("COULD NOT LOOK: the binary would not build here (%v):\n%s", err, out)
	}

	t.Run("an unknown command exits non-zero", func(t *testing.T) {
		cmd := exec.Command(bin, "no-such-command")
		out, err := cmd.CombinedOutput()

		require.Error(t, err, "a failure must not exit 0 — that is the whole defect")
		var ee *exec.ExitError
		require.ErrorAs(t, err, &ee)
		assert.NotEqual(t, 0, ee.ExitCode())
		assert.Contains(t, strings.ToLower(string(out)), "unknown command")
	})

	t.Run("the error goes to STDERR, not stdout", func(t *testing.T) {
		// A caller capturing stdout to parse it got the error text mixed into
		// whatever it was reading. Separating the streams is half the fix.
		cmd := exec.Command(bin, "no-such-command")
		var stdout, stderr strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		_ = cmd.Run()

		assert.Contains(t, strings.ToLower(stderr.String()), "unknown command")
		assert.NotContains(t, strings.ToLower(stdout.String()), "unknown command",
			"stdout must stay clean for whatever is parsing it")
	})

	t.Run("the error is printed ONCE, not twice", func(t *testing.T) {
		// ⛔ THIS IS WHAT SilenceErrors BUYS, and the first version of this test
		// could not see it: asserting "contains" passes whether the line appears
		// once or three times. mutate-check reported the flag BLIND, and the
		// direct measurement showed why —  with it off, cobra prints the error,
		// then the usage hint, then main() prints the error again.
		cmd := exec.Command(bin, "no-such-command")
		out, _ := cmd.CombinedOutput()

		n := strings.Count(string(out), "unknown command")
		assert.Equal(t, 1, n, "cobra and main() must not both report it; got:\n%s", out)
	})

	t.Run("CONTROL: a command that SUCCEEDS still exits 0", func(t *testing.T) {
		// Without this, "always exit 1" would pass every case above.
		cmd := exec.Command(bin, "version")
		err := cmd.Run()

		assert.NoError(t, err, "success must stay success")
	})

	t.Run("CONTROL: --help exits 0 and DOES list commands", func(t *testing.T) {
		// Silencing usage on errors must not silence the help someone asked for.
		cmd := exec.Command(bin, "--help")
		out, err := cmd.CombinedOutput()

		assert.NoError(t, err)
		assert.Contains(t, string(out), "Available Commands:",
			"asking for help must still produce help")
	})

	_ = os.Stdout
}
