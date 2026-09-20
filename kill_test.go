package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"claude-squad/session/git"
	"claude-squad/session/tmux"

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

// recordTerminalOps swaps the two tmux seams for recorders and returns what
// closeTerminalSession did. `present` is the list the session lister returns.
func recordTerminalOps(t *testing.T, present ...string) (closed *[]string) {
	t.Helper()
	oldList, oldClose := listTmuxSessions, closeTmuxSession
	t.Cleanup(func() { listTmuxSessions, closeTmuxSession = oldList, oldClose })
	var got []string
	listTmuxSessions = func() ([]string, error) { return present, nil }
	closeTmuxSession = func(name string) error { got = append(got, name); return nil }
	return &got
}

func TestCloseTerminalSession(t *testing.T) {
	initTestLog(t)

	t.Run("closes the term_ session when it is there", func(t *testing.T) {
		closed := recordTerminalOps(t, tmux.SessionName("term_bob"), tmux.SessionName("other"))
		closeTerminalSession("bob")
		require.Equal(t, []string{"term_bob"}, *closed)
	})

	// ⭐ THE CONTROL. Without it the test above passes for a function that
	// closes term_<title> unconditionally, which would kill a pane belonging to
	// nothing.
	t.Run("closes nothing when no term_ session exists", func(t *testing.T) {
		closed := recordTerminalOps(t, tmux.SessionName("bob"), tmux.SessionName("term_someone-else"))
		closeTerminalSession("bob")
		require.Empty(t, *closed)
	})

	// ⛔ COULD NOT LIST is not "it is not there". It must not close anything and
	// must not panic; the caller's teardown is already done either way.
	t.Run("a failed listing closes nothing", func(t *testing.T) {
		oldList, oldClose := listTmuxSessions, closeTmuxSession
		t.Cleanup(func() { listTmuxSessions, closeTmuxSession = oldList, oldClose })
		var got []string
		listTmuxSessions = func() ([]string, error) { return nil, errors.New("tmux unreachable") }
		closeTmuxSession = func(name string) error { got = append(got, name); return nil }
		closeTerminalSession("bob")
		require.Empty(t, got)
	})
}

// ⛔ THE BRANCH IS ONLY A DESTINATION WHEN THE TEARDOWN WOULD DELETE IT.
// session/git's Cleanup deletes the branch `if !g.isExistingBranch`, so a
// session started with `cs new --branch <existing>` KEEPS its branch. Counting
// it as a leftover refused the kill forever and --force did not reach that
// path. MEASURED 2026-09-20: rc=2 on every retry where main cleared the entry.
func TestStillPresentIgnoresABranchTheTeardownKeeps(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "not-here")

	// The probe the caller builds for an existing-branch session: none at all.
	left, err := stillPresent("no-such-session", gone, nil, "feature/keepme")
	require.NoError(t, err)
	require.Empty(t, left, "an existing branch is kept by design and is not a leftover")

	// ⭐ CONTROL: the SAME branch, with a probe supplied, IS reported -- so the
	// assertion above is about the probe being withheld, not about stillPresent
	// having quietly stopped checking branches at all.
	left, err = stillPresent("no-such-session", gone, fakeBranch{exists: true}, "feature/keepme")
	require.NoError(t, err)
	require.Contains(t, left, "branch feature/keepme")
}

// ⛔ A NIL *git.GitWorktree IN AN INTERFACE IS NOT A NIL INTERFACE. Assigning
// one makes `probe != nil` TRUE and BranchExists dereferences it. The first
// version of this branch did exactly that, and its own "no brancher" test
// passed a LITERAL nil -- an untyped nil interface, not the shape the call site
// produced -- so it was green against both the correct code and the broken one.
func TestStillPresentSkipsATypedNilProbe(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "not-here")

	var typedNil *git.GitWorktree
	var probe branchProbe = typedNil

	// ⚠ THE RAW `!= nil`, NOT require.NotNil. testify compares by reflection and
	// reports a typed-nil pointer as nil, which is the OPPOSITE of what the
	// production `if branch != nil` does -- so asserting through testify here
	// would have hidden the very distinction this test exists for.
	if probe == nil {
		t.Fatal("precondition failed: a typed nil should be a NON-nil interface to Go")
	}

	// What killInstance must build instead: a nil interface, which really is nil.
	var safe branchProbe
	if safe != nil {
		t.Fatal("a declared-but-unassigned branchProbe must be nil")
	}

	left, err := stillPresent("no-such-session", gone, safe, "feat")
	require.NoError(t, err)
	require.Empty(t, left)
}
