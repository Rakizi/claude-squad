package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"claude-squad/config"
	"claude-squad/session"
	"claude-squad/session/tmux"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These drive killInstance and pauseInstance THROUGH stored state, the way
// the CLI does, so that deleting the proof call from either command goes red
// (PR #3 review: "the proof is never reached; its own tests still pass").
//
// ⚠ They touch the REAL tmux server, read-only except for one `kill-session`
// on a title that cannot exist: session code restores through
// tmux.NewTmuxSession with the real executor. Titles carry the test pid so
// two runs cannot collide, and CLAUDE_SQUAD_DIR is a scratch directory so the
// real state.json is never read or written.

// storeEntries writes the given InstanceData as the whole stored state and
// returns a reader for what is on disk afterwards.
func storeEntries(t *testing.T, ds ...session.InstanceData) (stored func() []session.InstanceData) {
	t.Helper()
	t.Setenv(config.ConfigDirEnvVar, t.TempDir())
	raw, err := json.Marshal(ds)
	require.NoError(t, err)
	require.NoError(t, config.LoadState().SaveInstances(raw))
	return func() []session.InstanceData {
		var out []session.InstanceData
		if raw := config.LoadState().GetInstances(); len(raw) > 0 {
			require.NoError(t, json.Unmarshal(raw, &out))
		}
		return out
	}
}

func quietCmd() *cobra.Command {
	c := &cobra.Command{}
	c.SetOut(&bytes.Buffer{})
	return c
}

func TestKillInstanceWiring(t *testing.T) {
	initTestLog(t)
	repo, _ := killRepo(t)
	title := fmt.Sprintf("cs3-wiring-kill-%d", os.Getpid())
	gone := filepath.Join(t.TempDir(), "worktree-that-was-removed")
	// Paused: no tmux session and no worktree by design, which is the shape
	// LoadInstances can restore without a live server. The refs count alone
	// answers for it, and the missing tool proves agent-trace is not reached.
	t.Setenv(agentTraceEnv, filepath.Join(t.TempDir(), "must-not-run"))
	entry := session.InstanceData{
		Title: title, Status: session.Paused, Program: "true", Branch: "feat",
		Worktree: session.GitWorktreeData{RepoPath: repo, WorktreePath: gone, BranchName: "feat", SessionName: title},
	}
	// ⛔ THE DISCRIMINATION CONTROL the ticket mandates: a second, untouched
	// entry that must SURVIVE the kill. "A prune that deleted everything
	// passes the first assertion alone" (Rakizi/the-lab#30; review
	// 5194349086 §3 found exactly that mutant surviving).
	bystander := entry
	bystander.Title = title + "-bystander"
	bystander.Worktree.SessionName = bystander.Title
	stored := storeEntries(t, entry, bystander)
	titles := func() []string {
		var out []string
		for _, d := range stored() {
			out = append(out, d.Title)
		}
		return out
	}

	// Through the cobra RunE, not killInstance directly, so the flag-to-call
	// wiring (`killInstance(command, title, killForce)`) is on the tested
	// path as well.
	runKill := func(force bool) (stdout string, err error) {
		killYes, killForce = true, force
		defer func() { killYes, killForce = false, false }()
		return captureStdout(t, func() error { return killCmd.RunE(quietCmd(), []string{title}) })
	}

	t.Run("two unpushed commits: refused, exit 2, entry and branch untouched", func(t *testing.T) {
		out, err := runKill(false)
		require.Error(t, err)
		assert.Equal(t, exitRefused, exitCodeFor(err))
		assert.NotContains(t, out, "removed")
		assert.ElementsMatch(t, []string{title, bystander.Title}, titles(), "a refusal must remove nothing from state")
		gitq(t, repo, "show-ref", "--verify", "--quiet", "refs/heads/feat")
	})

	t.Run("--force: removed, the override is on stdout, entry and branch gone", func(t *testing.T) {
		// ⛔ THE --force RECORDING. Both reviewers' mutants that made --force a
		// silent no-op, or made it stop recording, stayed green before this.
		out, err := runKill(true)
		require.NoError(t, err)
		assert.Contains(t, out, title+"\tremoved\tFORCED past exit 2")
		assert.Contains(t, out, "2 commit(s)")
		assert.Equal(t, []string{bystander.Title}, titles(),
			"the killed entry is ABSENT and the untouched one is PRESENT")
		if ok, _ := gitBranchExists(repo, "feat"); ok {
			t.Fatal("a forced kill must delete the branch, or the kill did not happen")
		}
	})
}

// captureStdout runs fn with os.Stdout redirected to a pipe. killCmd prints
// its result with fmt.Printf (upstream shape), so this is the only way to read
// it.
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	orig := os.Stdout
	os.Stdout = w
	runErr := fn()
	os.Stdout = orig
	require.NoError(t, w.Close())
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	return buf.String(), runErr
}

func gitBranchExists(repo, branch string) (bool, error) {
	c := gitCmd(repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	err := c.Run()
	return err == nil, err
}

func TestPauseInstanceWiring(t *testing.T) {
	initTestLog(t)
	repo, _ := killRepo(t)
	gone := filepath.Join(t.TempDir(), "worktree-that-was-removed")

	t.Run("branch missing: refused, exit 2, state untouched", func(t *testing.T) {
		// A Running entry whose tmux does not exist restores as Unknown; the
		// proof runs before Pause() and must refuse, leaving the stored
		// status as it was (a refusal persists nothing).
		title := fmt.Sprintf("cs3-wiring-pause-missing-%d", os.Getpid())
		stored := storeEntries(t, session.InstanceData{
			Title: title, Status: session.Running, Program: "true", Branch: "no-such-branch",
			Worktree: session.GitWorktreeData{RepoPath: repo, WorktreePath: gone, BranchName: "no-such-branch", SessionName: title},
		})
		err := pauseInstance(title)
		require.Error(t, err)
		assert.Equal(t, exitRefused, exitCodeFor(err))
		require.Len(t, stored(), 1)
		assert.Equal(t, session.Running, stored()[0].Status, "a refused pause must not rewrite the stored status")
	})

	t.Run("branch present: paused, stored status is Paused -- the positive control", func(t *testing.T) {
		title := fmt.Sprintf("cs3-wiring-pause-ok-%d", os.Getpid())
		stored := storeEntries(t, session.InstanceData{
			Title: title, Status: session.Running, Program: "true", Branch: "feat",
			Worktree: session.GitWorktreeData{RepoPath: repo, WorktreePath: gone, BranchName: "feat", SessionName: title},
		})
		require.NoError(t, pauseInstance(title))
		require.Len(t, stored(), 1)
		assert.Equal(t, session.Paused, stored()[0].Status)
	})
}

// ⛔ A REFUSED KILL MUST NOT CLOSE THE TERMINAL PANE. Driven through killCmd.RunE
// so the ORDER of closeTerminalSession relative to the pre-kill proof and the
// teardown is on the tested path.
//
// Both mutants that this exists for compiled and left the whole suite green:
// deleting closeTerminalSession outright, and hoisting it in front of
// preKillProof. An earlier revision of kill.go also put the call before the
// teardown, where a post-Kill refusal (exit 2) had already closed a pane the
// caller was told was untouched.
func TestKillDoesNotTouchTheTerminalOnARefusal(t *testing.T) {
	initTestLog(t)
	repo, _ := killRepo(t)
	title := fmt.Sprintf("cs6-term-order-%d", os.Getpid())
	gone := filepath.Join(t.TempDir(), "worktree-that-was-removed")
	t.Setenv(agentTraceEnv, filepath.Join(t.TempDir(), "must-not-run"))
	storeEntries(t, session.InstanceData{
		Title: title, Status: session.Paused, Program: "true", Branch: "feat",
		Worktree: session.GitWorktreeData{RepoPath: repo, WorktreePath: gone, BranchName: "feat", SessionName: title},
	})

	closed := recordTerminalOps(t, tmux.SessionName("term_"+title))

	runKill := func(force bool) error {
		killYes, killForce = true, force
		defer func() { killYes, killForce = false, false }()
		_, err := captureStdout(t, func() error { return killCmd.RunE(quietCmd(), []string{title}) })
		return err
	}

	err := runKill(false)
	require.Error(t, err)
	require.Equal(t, exitRefused, exitCodeFor(err))
	require.Empty(t, *closed,
		"the kill was REFUSED, so the caller was told nothing was removed -- "+
			"closing their terminal pane makes that statement false")

	// ⭐ THE CONTROL. Without it this passes for a build that never closes the
	// terminal at all, which is the defect the call was added to fix.
	require.NoError(t, runKill(true))
	require.Equal(t, []string{"term_" + title}, *closed,
		"a kill that actually happened must close the term_ session")
}

// ⛔ THE FIX FOR THE PERMANENT STUCK ENTRY IS A ONE-LINE DECISION IN
// killInstance, AND NOTHING TESTED IT. `stillPresent` is covered in isolation
// with a hand-passed probe; what was uncovered is whether killInstance builds
// the right probe. Both flips of `case wt.IsExistingBranch:` compiled and left
// the suite green, and each reinstated a shipped defect:
//
//	case false:  the existing-branch session is stuck again -- rc=2 forever
//	case true:   an ordinary session's surviving branch stops being counted,
//	             which is defect #1 of this PR, back.
func TestKillCountsTheBranchOnlyWhenTheTeardownWouldDeleteIt(t *testing.T) {
	initTestLog(t)
	repo, _ := killRepo(t)
	gone := filepath.Join(t.TempDir(), "worktree-that-was-removed")

	// ⚠ `feat` is CHECKED OUT in the main repo, so `git branch -D feat` cannot
	// succeed. That is the only way the branch survives a teardown that meant
	// to delete it -- and it is the real shape: `pause` copies the branch name
	// to the clipboard precisely so you can check it out.
	gitq(t, repo, "checkout", "-q", "feat")

	// The worktree is gone, so Kill() fails and stillPresent decides the
	// outcome. Only the IsExistingBranch flag differs between the two cases.
	entry := func(title string, existing bool) session.InstanceData {
		return session.InstanceData{
			Title: title, Status: session.Paused, Program: "true", Branch: "feat",
			Worktree: session.GitWorktreeData{
				RepoPath: repo, WorktreePath: gone, BranchName: "feat",
				SessionName: title, IsExistingBranch: existing,
			},
		}
	}
	runKill := func(title string) error {
		killYes, killForce = true, true // --force: past the pre-kill proof, not past this
		defer func() { killYes, killForce = false, false }()
		_, err := captureStdout(t, func() error { return killCmd.RunE(quietCmd(), []string{title}) })
		return err
	}

	t.Run("existing branch: kept by design, so NOT a leftover", func(t *testing.T) {
		title := fmt.Sprintf("cs6-existing-%d", os.Getpid())
		stored := storeEntries(t, entry(title, true))
		err := runKill(title)
		require.NoError(t, err, "cs new --branch <existing> keeps its branch; "+
			"counting it as a leftover refuses the kill forever and --force does not reach this path")
		require.Empty(t, stored(), "the entry must clear")
	})

	// ⭐ THE CONTROL, and it is the whole point: the SAME branch, the same
	// failure, only the flag differs. Without it the test above passes for a
	// build that stopped counting branches at all.
	t.Run("generated branch: deleted by the teardown, so it IS a leftover", func(t *testing.T) {
		title := fmt.Sprintf("cs6-generated-%d", os.Getpid())
		stored := storeEntries(t, entry(title, false))
		err := runKill(title)
		require.Error(t, err)
		assert.Equal(t, exitRefused, exitCodeFor(err))
		require.Len(t, stored(), 1, "a refused kill must leave the entry alone")
	})
}

// ⛔ `pause` IS HALF OF THE TERMINAL-ORPHAN FIX AND ONLY `kill` WAS COVERED.
// Deleting closeTerminalSession from pause.go alone compiled and stayed green.
func TestPauseClosesTheTerminalSession(t *testing.T) {
	initTestLog(t)
	repo, _ := killRepo(t)
	title := fmt.Sprintf("cs6-pause-term-%d", os.Getpid())
	gone := filepath.Join(t.TempDir(), "worktree-that-was-removed")
	storeEntries(t, session.InstanceData{
		Title: title, Status: session.Running, Program: "true", Branch: "feat",
		Worktree: session.GitWorktreeData{RepoPath: repo, WorktreePath: gone, BranchName: "feat", SessionName: title},
	})

	closed := recordTerminalOps(t, tmux.SessionName("term_"+title))
	require.NoError(t, pauseInstance(title))
	require.Equal(t, []string{"term_" + title}, *closed,
		"pause removes the worktree, so a surviving term_ session sits in a directory that is gone")
}

// ⛔ AND A REFUSED PAUSE MUST LEAVE THE PANE ALONE. kill.go has this test;
// pause.go did not, so the exact defect kill.go's own comment documents --
// "an earlier revision put the call before the teardown, where a refusal had
// already closed a pane the caller was told was untouched" -- could be
// reintroduced in pause.go and ship green. Two hoists were found that way.
func TestPauseDoesNotTouchTheTerminalOnARefusal(t *testing.T) {
	initTestLog(t)
	repo, _ := killRepo(t)
	title := fmt.Sprintf("cs6-pause-refuse-%d", os.Getpid())
	gone := filepath.Join(t.TempDir(), "worktree-that-was-removed")
	// A branch that does not exist: prePauseProof refuses before Pause() runs.
	stored := storeEntries(t, session.InstanceData{
		Title: title, Status: session.Running, Program: "true", Branch: "no-such-branch",
		Worktree: session.GitWorktreeData{RepoPath: repo, WorktreePath: gone,
			BranchName: "no-such-branch", SessionName: title},
	})

	closed := recordTerminalOps(t, tmux.SessionName("term_"+title))
	err := pauseInstance(title)
	require.Error(t, err)
	assert.Equal(t, exitRefused, exitCodeFor(err))
	require.Empty(t, *closed,
		"the pause was REFUSED, so the caller was told nothing changed -- "+
			"closing their terminal pane makes that statement false")
	assert.Equal(t, session.Running, stored()[0].Status, "a refusal persists nothing")
}
