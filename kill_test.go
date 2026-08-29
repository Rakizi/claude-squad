package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestKillPrunesStateEndToEnd is Rakizi/the-lab#29's acceptance check, run the
// way the ticket asks for it: `ls` in a NEW PROCESS after the kill, not a
// re-read inside the one that did it.
//
// ⛔ AND IT NEEDS THE DISCRIMINATION CONTROL, or the check looks right while
// measuring nothing: the killed session must be ABSENT and an untouched one
// must still be PRESENT. A prune that emptied the store would satisfy the first
// assertion on its own, and so would a `kill` that deleted the state file.
//
// Every assertion asks the DESTINATION -- the state file through a fresh `ls`,
// tmux through `tmux ls`, the worktree through the filesystem -- because the
// exit code of a teardown command is not evidence that the thing is gone.
func TestKillPrunesStateEndToEnd(t *testing.T) {
	for _, bin := range []string{"git", "tmux"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("COULD NOT LOOK: %s is not on PATH (%v); this exercises the real "+
				"teardown and there is nothing to substitute for it", bin, err)
		}
	}

	cs := filepath.Join(t.TempDir(), "cs-test")
	if out, err := exec.Command("go", "build", "-o", cs, ".").CombinedOutput(); err != nil {
		t.Skipf("COULD NOT LOOK: the binary would not build here (%v):\n%s", err, out)
	}

	csDir := t.TempDir()
	repo := t.TempDir()

	run := func(t *testing.T, name string, args ...string) (string, int) {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Env = append(os.Environ(), "CLAUDE_SQUAD_DIR="+csDir)
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		code := 0
		if err != nil {
			var ee *exec.ExitError
			if !assert.ErrorAs(t, err, &ee) {
				t.Fatalf("%s %v: %v", name, args, err)
			}
			code = ee.ExitCode()
		}
		return string(out), code
	}

	// A throwaway repo with one commit -- enough for a worktree.
	for _, args := range [][]string{
		{"init", "-q", "-b", "main", "."},
		{"config", "user.email", "kill-test@example.invalid"},
		{"config", "user.name", "kill-test"},
		{"commit", "-q", "--allow-empty", "-m", "init"},
	} {
		if out, code := run(t, "git", args...); code != 0 {
			t.Skipf("COULD NOT LOOK: could not build a throwaway repo (git %v -> %d):\n%s",
				args, code, out)
		}
	}

	// Titles carry the PID so a parallel run, or a real session on this machine,
	// cannot be the thing the assertions see.
	keep := fmt.Sprintf("csKeep%d", os.Getpid())
	gone := fmt.Sprintf("csGone%d", os.Getpid())

	tmuxSession := func(title string) string { return "claudesquad_" + title }
	t.Cleanup(func() {
		for _, title := range []string{keep, gone} {
			_ = exec.Command("tmux", "kill-session", "-t", tmuxSession(title)).Run()
		}
	})

	for _, title := range []string{keep, gone} {
		out, code := run(t, cs, "new", title, "--repo", repo, "--program", "sleep 120")
		if code != 0 {
			t.Skipf("COULD NOT LOOK: `new %s` exited %d, so there is nothing to kill:\n%s",
				title, code, out)
		}
	}

	// What a FRESH process reads out of the store.
	type view struct {
		Title    string `json:"title"`
		Worktree string `json:"worktree"`
	}
	listed := func(t *testing.T) map[string]view {
		t.Helper()
		out, code := run(t, cs, "ls", "--json")
		require.Equal(t, 0, code, "`ls --json` must succeed:\n%s", out)
		var rows []view
		require.NoError(t, json.Unmarshal([]byte(out), &rows), "ls output:\n%s", out)
		byTitle := make(map[string]view, len(rows))
		for _, r := range rows {
			byTitle[r.Title] = r
		}
		return byTitle
	}

	before := listed(t)
	require.Contains(t, before, keep, "setup: both sessions must exist before the kill")
	require.Contains(t, before, gone, "setup: both sessions must exist before the kill")
	keptWorktree := before[keep].Worktree
	goneWorktree := before[gone].Worktree
	require.NotEmpty(t, keptWorktree)
	require.NotEmpty(t, goneWorktree)

	out, code := run(t, cs, "kill", gone, "--yes")
	require.Equal(t, exitOK, code, "kill must succeed:\n%s", out)

	after := listed(t)

	assert.NotContains(t, after, gone,
		"Rakizi/the-lab#29: the killed session must be absent from a FRESH `ls --json`")
	assert.Contains(t, after, keep,
		"DISCRIMINATION: an untouched session must survive -- a kill that emptied "+
			"the store, or deleted the state file, would pass the assertion above alone")

	// The destinations, not the exit code.
	tmuxOut, _ := run(t, "tmux", "ls")
	assert.NotContains(t, tmuxOut, tmuxSession(gone), "the killed tmux session must be gone")
	assert.Contains(t, tmuxOut, tmuxSession(keep),
		"DISCRIMINATION: the untouched tmux session must still be there")

	_, err := os.Stat(goneWorktree)
	assert.True(t, os.IsNotExist(err), "the killed worktree must be removed, got err=%v", err)
	_, err = os.Stat(keptWorktree)
	assert.NoError(t, err, "DISCRIMINATION: the untouched worktree must still be on disk")

	t.Run("REFUSED: killing an unknown title exits 2 and removes nothing", func(t *testing.T) {
		out, code := run(t, cs, "kill", "no-such-session-"+gone, "--yes")
		assert.Equal(t, exitRefused, code,
			"a title that does not exist is a refusal, not a could-not-look:\n%s", out)
		assert.Contains(t, listed(t), keep, "a refused kill must leave the store alone")
	})

	t.Run("REFUSED: --yes is required", func(t *testing.T) {
		out, code := run(t, cs, "kill", keep)
		assert.Equal(t, exitRefused, code, "kill without --yes must refuse:\n%s", out)
		assert.Contains(t, strings.ToLower(out), "--yes")
		assert.Contains(t, listed(t), keep, "a refused kill must not tear anything down")
	})
}
