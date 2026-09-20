package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"claude-squad/session/git"

	"github.com/stretchr/testify/require"
)

// fakeBranch drives stillPresent's branch leg without building a repository.
type fakeBranch struct {
	exists bool
	err    error
}

func (f fakeBranch) BranchExists() (bool, error) { return f.exists, f.err }

// ⛔ THE NEGATIVE HALVES ARE THE LOAD-BEARING ONES. stillPresent checked the
// tmux session and the worktree PATH only, and a branch that could NOT be
// deleted left both of those clean -- so `cs kill` printed the branch error,
// concluded "already torn down", removed the state entry and EXITED 0.
//
// MEASURED 2026-09-20: pause a session, check its branch out in the main repo
// (exactly what pause invites you to do -- it copies the branch name to the
// clipboard), then kill it:
//
//	cslab-k2  already torn down (... cannot delete branch 'rakizi/cslab-k2'
//	          used by worktree at ...)
//	cslab-k2  removed
//	kill rc=0                    <- and `git branch` still listed it
func TestStillPresentCountsTheBranch(t *testing.T) {
	// A worktree path that does not exist and a title with no tmux session, so
	// the BRANCH is the only leg that can report anything -- the exact shape of
	// the measured bug.
	gone := filepath.Join(t.TempDir(), "not-here")

	t.Run("surviving branch is reported", func(t *testing.T) {
		left, err := stillPresent("no-such-session", gone, fakeBranch{exists: true}, "rakizi/leftover")
		require.NoError(t, err)
		require.Contains(t, left, "branch rakizi/leftover")
	})

	t.Run("absent branch leaves nothing behind", func(t *testing.T) {
		left, err := stillPresent("no-such-session", gone, fakeBranch{exists: false}, "rakizi/leftover")
		require.NoError(t, err)
		require.Empty(t, left)
	})

	// ⭐ COULD NOT LOOK is NOT absent. Reporting it as "nothing left" is how a
	// real leftover gets called clean and the entry is dropped anyway.
	t.Run("could not look propagates, never reads as absent", func(t *testing.T) {
		boom := errors.New("not a repository")
		left, err := stillPresent("no-such-session", gone, fakeBranch{err: boom}, "rakizi/leftover")
		require.ErrorIs(t, err, boom)
		require.Nil(t, left)
	})

	t.Run("no brancher: the leg is skipped, not guessed", func(t *testing.T) {
		left, err := stillPresent("no-such-session", gone, nil, "rakizi/leftover")
		require.NoError(t, err)
		require.Empty(t, left)
	})
}

// The real GitWorktree.BranchExists is what ships; pin that stillPresent works
// against it and not only against the fake.
func TestStillPresentAgainstARealRepository(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", dir},
		{"-C", dir, "config", "user.email", "t@t"},
		{"-C", dir, "config", "user.name", "t"},
	} {
		require.NoError(t, exec.Command("git", args...).Run(), "git %v", args)
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hi\n"), 0o644))
	require.NoError(t, exec.Command("git", "-C", dir, "add", "a.txt").Run())
	require.NoError(t, exec.Command("git", "-C", dir, "commit", "-qm", "init").Run())
	require.NoError(t, exec.Command("git", "-C", dir, "branch", "rakizi/leftover").Run())

	gw := git.NewGitWorktreeFromStorage(dir, filepath.Join(dir, "wt"), "s", "rakizi/leftover", "", false)
	gone := filepath.Join(t.TempDir(), "not-here")

	left, err := stillPresent("no-such-session", gone, gw, "rakizi/leftover")
	require.NoError(t, err)
	require.Contains(t, left, "branch rakizi/leftover")

	require.NoError(t, exec.Command("git", "-C", dir, "branch", "-D", "rakizi/leftover").Run())
	left, err = stillPresent("no-such-session", gone, gw, "rakizi/leftover")
	require.NoError(t, err)
	require.Empty(t, left, "with the branch deleted there is nothing left")
}
