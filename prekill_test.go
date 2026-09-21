package main

import (
	"claude-squad/log"
	"claude-squad/session"
	"claude-squad/session/git"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// initTestLog gives recordForcedKill a logger; session code panics on the nil
// one otherwise -- the same trap kill.go's own comment records.
func initTestLog(t *testing.T) {
	t.Helper()
	log.Initialize(false)
	t.Cleanup(log.Close)
}

// fakeAgentTrace writes an executable that prints `body` to stdout and exits
// with `code`, and points the env override at it for the rest of the test.
func fakeAgentTrace(t *testing.T, body string, code int) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "agent-trace")
	script := "#!/bin/sh\ncat <<'JSON'\n" + body + "\nJSON\nexit " + strconv.Itoa(code) + "\n"
	require.NoError(t, os.WriteFile(p, []byte(script), 0755))
	t.Setenv(agentTraceEnv, p)
}

const wt = "/tmp/wt/w-x-1_abc"

func row(state, worktree string) string {
	return `{"repos_searched": 8, "sessions": [{"session": "w-x-1", "worktree": "` + worktree +
		`", "state": "` + state + `", "verdict": "v", "blind": ["b1"]}]}`
}

func TestJudgeTrace(t *testing.T) {
	t.Run("LOCAL_ONLY_WORK is REFUSED, exit 2", func(t *testing.T) {
		// ⛔ THE LOAD-BEARING HALF. A kill on this state destroys commits that
		// exist nowhere else.
		err := judgeTrace(&traceRow{State: traceLocalOnlyWork, Verdict: "2 commits only here"}, nil)
		require.Error(t, err)
		assert.Equal(t, exitRefused, exitCodeFor(err))
		assert.Contains(t, err.Error(), "2 commits only here")
	})

	t.Run("CANNOT_TELL is could-not-look, exit 3 -- never a pass", func(t *testing.T) {
		err := judgeTrace(&traceRow{State: traceCannotTell, Blind: []string{"no worktree found"}}, nil)
		require.Error(t, err)
		assert.Equal(t, exitCouldNotLook, exitCodeFor(err))
		assert.Contains(t, err.Error(), "no worktree found")
	})

	t.Run("a check that could not run is exit 3, not a pass", func(t *testing.T) {
		err := judgeTrace(nil, errors.New("agent-trace not on PATH"))
		require.Error(t, err)
		assert.Equal(t, exitCouldNotLook, exitCodeFor(err))
	})

	t.Run("LANDED proceeds -- the positive control", func(t *testing.T) {
		assert.NoError(t, judgeTrace(&traceRow{State: "LANDED"}, nil))
	})

	t.Run("DECISION_UNRELAYED and UNFINISHED proceed: the state gates, not exit 1", func(t *testing.T) {
		// ⚠ THE AUDIT CORRECTION on Rakizi/the-lab#30. agent-trace exits 1 for
		// these too. Keying the refusal on its exit code would block every
		// kill of a session that merely ended on a question.
		assert.NoError(t, judgeTrace(&traceRow{State: "DECISION_UNRELAYED"}, nil))
		assert.NoError(t, judgeTrace(&traceRow{State: "UNFINISHED"}, nil))
	})

	t.Run("the two refusals carry DIFFERENT codes", func(t *testing.T) {
		// One code for both would let a script treat "could not check" as
		// "found unpushed work" -- or the reverse.
		a := exitCodeFor(judgeTrace(&traceRow{State: traceLocalOnlyWork}, nil))
		b := exitCodeFor(judgeTrace(&traceRow{State: traceCannotTell}, nil))
		assert.NotEqual(t, a, b)
	})

	t.Run("a state this build does not know is exit 3, never a pass", func(t *testing.T) {
		// ⛔ FAIL CLOSED. The switch used to fall through to "proceed" on any
		// unrecognised word -- an empty state, a renamed one, a different
		// agent-trace earlier on PATH -- in front of `git branch -D`
		// (review 5194349086 §4).
		for _, state := range []string{"", "SOME_NEW_STATE", "DIRTY_WORKTREE"} {
			err := judgeTrace(&traceRow{State: state}, nil)
			require.Error(t, err, "state %q must not proceed", state)
			assert.Equal(t, exitCouldNotLook, exitCodeFor(err))
			assert.Contains(t, err.Error(), "does not know")
		}
	})
}

func TestJudgeLocalOnly(t *testing.T) {
	assert.NoError(t, judgeLocalOnly(0, nil), "clean branch proceeds")

	err := judgeLocalOnly(1, nil)
	require.Error(t, err, "one unpushed commit MUST refuse")
	assert.Equal(t, exitRefused, exitCodeFor(err))
	assert.Contains(t, err.Error(), "no remote and no tag")

	err = judgeLocalOnly(0, errors.New("not a git repository"))
	require.Error(t, err, "a count that could not run MUST refuse, even though n is 0")
	assert.Equal(t, exitCouldNotLook, exitCodeFor(err))
}

// TestFreshLocalOnlyGoneBranch covers the reap path against a REAL repository,
// because the defect lived in what git does to an absent ref rather than in
// any decision a fake could model.
//
// ⛔ MEASURED 2026-09-21: 11 of 25 failed reaps were "ambiguous argument
// 'refs/heads/rakizi/w-nag-XXXX'" -- `rev-list` on a branch that was already
// deleted. That surfaced as could-not-look and `cs kill` refused, so the slots
// could never be freed, while the work sat safely on origin the whole time. A
// branch that is gone cannot be holding unpushed work, so it is the SAFEST
// case, not an unreadable one.
func TestFreshLocalOnlyGoneBranch(t *testing.T) {
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main", ".")
	run("commit", "-q", "--allow-empty", "-m", "base")

	t.Run("a branch that exists and is unpushed still REFUSES -- the control", func(t *testing.T) {
		// ⛔ MUST-HIT. Without this, a fix that always returned (0, nil) would
		// pass the gone-branch case below and silently disarm the whole check.
		run("branch", "held")
		run("commit", "-q", "--allow-empty", "-m", "only-here")
		run("branch", "-f", "held", "HEAD")
		wt := git.NewGitWorktreeFromStorage(repo, "", "s-held", "held", "", false)
		n, err := freshLocalOnly(wt)
		require.NoError(t, err, "an existing branch must be countable")
		require.Greater(t, n, 0, "an unpushed commit must still be counted")
		require.Error(t, judgeLocalOnly(n, err), "and must still refuse the kill")
	})

	t.Run("a branch that is GONE counts 0 and proceeds", func(t *testing.T) {
		wt := git.NewGitWorktreeFromStorage(repo, "", "s-gone", "rakizi/w-nag-9999", "", false)
		n, err := freshLocalOnly(wt)
		require.NoError(t, err,
			"a deleted branch is not unreadable -- it is the safest case; "+
				"before the fix this was `ambiguous argument` and blocked the reap")
		assert.Equal(t, 0, n)
		assert.NoError(t, judgeLocalOnly(n, err), "and the kill proceeds")
	})

	t.Run("an unreadable repository is STILL could-not-look", func(t *testing.T) {
		// ⛔ The fix must not soften a genuine failure into "nothing to lose".
		wt := git.NewGitWorktreeFromStorage(filepath.Join(t.TempDir(), "nope"), "", "s-broken", "any", "", false)
		n, err := freshLocalOnly(wt)
		require.Error(t, err, "an unreadable repo must refuse, not count 0")
		assert.Equal(t, exitCouldNotLook, exitCodeFor(judgeLocalOnly(n, err)))
	})
}

func TestRunAgentTrace(t *testing.T) {
	t.Run("a matching row comes back with its state, whatever the exit code", func(t *testing.T) {
		// exit 1 here: agent-trace's ACTION code, which the caller must NOT
		// treat as a refusal on its own.
		fakeAgentTrace(t, row("DECISION_UNRELAYED", wt), 1)
		r, err := runAgentTrace("w-x-1", wt)
		require.NoError(t, err)
		assert.Equal(t, "DECISION_UNRELAYED", r.State)
		assert.Equal(t, []string{"b1"}, r.Blind)
	})

	t.Run("a row for a DIFFERENT worktree is could-not-look", func(t *testing.T) {
		// agent-trace's lookup falls back to a substring match; "w-x-1" can
		// resolve to w-x-10's tree. Trusting that row would judge the wrong
		// session.
		fakeAgentTrace(t, row("LANDED", "/tmp/wt/w-x-10_def"), 0)
		_, err := runAgentTrace("w-x-1", wt)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not this session's worktree")
	})

	t.Run("no row for the title is could-not-look", func(t *testing.T) {
		fakeAgentTrace(t, `{"sessions": []}`, 0)
		_, err := runAgentTrace("w-x-1", wt)
		require.Error(t, err)
	})

	t.Run("prose instead of JSON is could-not-look, and carries the prose", func(t *testing.T) {
		fakeAgentTrace(t, "  ⛔ gh not on PATH — COULD NOT LOOK", 3)
		_, err := runAgentTrace("w-x-1", wt)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "gh not on PATH")
	})

	t.Run("a missing tool is could-not-look", func(t *testing.T) {
		t.Setenv(agentTraceEnv, filepath.Join(t.TempDir(), "absent"))
		_, err := runAgentTrace("w-x-1", wt)
		require.Error(t, err)
	})

	t.Run("a row for a DIFFERENT session title is not this session's row", func(t *testing.T) {
		// The other half of the lookup (PR #3 review N3): the worktree check
		// alone cannot catch a row whose worktree matches but whose title does
		// not, which is what a hand-edited or duplicated state entry produces.
		fakeAgentTrace(t, `{"sessions": [{"session": "w-x-2", "worktree": "`+wt+`", "state": "LANDED"}]}`, 0)
		_, err := runAgentTrace("w-x-1", wt)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no row for")
	})

	t.Run("an EMPTY recorded worktree path is refused before the tool runs", func(t *testing.T) {
		// PR #3 review N1: with no recorded path the equality check had
		// nothing to compare against and waved any row through. The check
		// is unconditional now: no path, no verification, no kill.
		fakeAgentTrace(t, row("LANDED", "/anything/at/all"), 0)
		_, err := runAgentTrace("w-x-1", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no worktree path")
	})
}

func TestRecordForcedKill(t *testing.T) {
	// The note names the code it stepped over, so a reader of stdout can tell
	// "forced past unpushed commits" from "forced past a check that never ran".
	initTestLog(t)
	note := recordForcedKill("w-x-1", refused("2 commit(s) on the branch exist on no remote"))
	assert.Contains(t, note, "FORCED past exit 2")
	assert.Contains(t, note, "no remote")

	note = recordForcedKill("w-x-1", couldNotLook("agent-trace not on PATH"))
	assert.Contains(t, note, "FORCED past exit 3")
}

// killRepo builds a clone whose branch `feat` has two commits that exist only
// locally, and returns (repo, push) where push() sends them to the remote.
func killRepo(t *testing.T) (repo string, push func()) {
	t.Helper()
	root := t.TempDir()
	bare := filepath.Join(root, "remote.git")
	gitq(t, root, "init", "-q", "--bare", "-b", "main", bare)
	repo = filepath.Join(root, "work")
	gitq(t, root, "clone", "-q", bare, repo)
	gitq(t, repo, "config", "user.email", "t@t")
	gitq(t, repo, "config", "user.name", "t")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\n"), 0644))
	gitq(t, repo, "add", "a.txt")
	gitq(t, repo, "commit", "-qm", "base")
	gitq(t, repo, "push", "-q", "origin", "main")
	gitq(t, repo, "checkout", "-q", "-b", "feat")
	for _, f := range []string{"b.txt", "c.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(repo, f), []byte(f+"\n"), 0644))
		gitq(t, repo, "add", f)
		gitq(t, repo, "commit", "-qm", "local "+f)
	}
	gitq(t, repo, "checkout", "-q", "main")
	return repo, func() { gitq(t, repo, "push", "-q", "origin", "feat") }
}

func gitCmd(dir string, args ...string) *exec.Cmd {
	c := exec.Command("git", args...)
	c.Dir = dir
	return c
}

func gitq(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := gitCmd(dir, args...)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}

// pausedTarget builds a started Instance with a git worktree but no tmux -- the
// shape FromInstanceData gives a Paused entry -- so preKillProof can be run
// against a real repository without a tmux server.
func pausedTarget(t *testing.T, repo, worktreePath string) *session.Instance {
	t.Helper()
	inst, err := session.FromInstanceData(session.InstanceData{
		Title: "w-x-1", Status: session.Paused, Program: "true",
		Worktree: session.GitWorktreeData{RepoPath: repo, WorktreePath: worktreePath, BranchName: "feat"},
	})
	require.NoError(t, err)
	return inst
}

// The proof end to end against a real repository. The negative halves are the
// point: an unpushed commit MUST refuse, an unreadable repo MUST refuse, and
// only a clean branch with a clean trace is allowed.
func TestPreKillProof(t *testing.T) {
	initTestLog(t)
	repo, push := killRepo(t)
	gone := filepath.Join(t.TempDir(), "worktree-that-was-removed")

	t.Run("unpushed commits are REFUSED before agent-trace is even consulted", func(t *testing.T) {
		t.Setenv(agentTraceEnv, filepath.Join(t.TempDir(), "must-not-run"))
		err := preKillProof(pausedTarget(t, repo, gone), "w-x-1", gone)
		require.Error(t, err)
		assert.Equal(t, exitRefused, exitCodeFor(err))
		assert.Contains(t, err.Error(), "2 commit(s)")
	})

	t.Run("an unreadable repository is exit 3, not a pass", func(t *testing.T) {
		err := preKillProof(pausedTarget(t, filepath.Join(t.TempDir(), "nope"), gone), "w-x-1", gone)
		require.Error(t, err)
		assert.Equal(t, exitCouldNotLook, exitCodeFor(err))
	})

	push()

	t.Run("pushed + worktree gone (paused): allowed on the refs count alone", func(t *testing.T) {
		// agent-trace has nothing to read without a worktree; pointing it at
		// a path that does not exist proves it was not consulted.
		t.Setenv(agentTraceEnv, filepath.Join(t.TempDir(), "must-not-run"))
		assert.NoError(t, preKillProof(pausedTarget(t, repo, gone), "w-x-1", gone))
	})

	present := t.TempDir()
	t.Run("pushed + worktree present: agent-trace LOCAL_ONLY_WORK still refuses", func(t *testing.T) {
		// The second, independent measurement. The refs say clean; the trace
		// says otherwise (it also reads the transcript). The trace wins.
		fakeAgentTrace(t, row(traceLocalOnlyWork, present), 1)
		err := preKillProof(pausedTarget(t, repo, present), "w-x-1", present)
		require.Error(t, err)
		assert.Equal(t, exitRefused, exitCodeFor(err))
	})

	t.Run("pushed + worktree present + agent-trace missing: exit 3", func(t *testing.T) {
		t.Setenv(agentTraceEnv, filepath.Join(t.TempDir(), "absent"))
		err := preKillProof(pausedTarget(t, repo, present), "w-x-1", present)
		require.Error(t, err)
		assert.Equal(t, exitCouldNotLook, exitCodeFor(err))
	})

	t.Run("pushed + worktree present + LANDED: allowed -- the positive control", func(t *testing.T) {
		fakeAgentTrace(t, row("LANDED", present), 0)
		assert.NoError(t, preKillProof(pausedTarget(t, repo, present), "w-x-1", present))
	})

	t.Run("an empty recorded worktree path is exit 3 even with a LANDED row", func(t *testing.T) {
		fakeAgentTrace(t, row("LANDED", ""), 0)
		err := preKillProof(pausedTarget(t, repo, ""), "w-x-1", "")
		require.Error(t, err)
		assert.Equal(t, exitCouldNotLook, exitCodeFor(err))
	})
}

// The remedy the refusal prints must clear the refusal. "Push OR TAG them
// first" -- so a tag, with nothing pushed, must turn exit 2 into a pass.
func TestPreKillProofTagIsAHarbour(t *testing.T) {
	initTestLog(t)
	repo, _ := killRepo(t)
	gone := filepath.Join(t.TempDir(), "worktree-that-was-removed")
	t.Setenv(agentTraceEnv, filepath.Join(t.TempDir(), "must-not-run"))

	err := preKillProof(pausedTarget(t, repo, gone), "w-x-1", gone)
	require.Error(t, err, "control: untagged, unpushed commits must refuse")
	assert.Equal(t, exitRefused, exitCodeFor(err))
	assert.Contains(t, err.Error(), "Push or tag")

	gitq(t, repo, "tag", "keep-w-x-1", "refs/heads/feat")
	assert.NoError(t, preKillProof(pausedTarget(t, repo, gone), "w-x-1", gone),
		"the operator did exactly what the message said; the refusal must clear")
}

// The refs count is taken against a REFRESHED view. A branch pushed from
// elsewhere -- present on the remote, unknown to this clone -- must not be
// refused as unpushed (PR #3 review K1).
func TestPreKillProofRefreshesBeforeCounting(t *testing.T) {
	initTestLog(t)
	repo, push := killRepo(t)
	gone := filepath.Join(t.TempDir(), "worktree-that-was-removed")
	t.Setenv(agentTraceEnv, filepath.Join(t.TempDir(), "must-not-run"))

	push()
	gitq(t, repo, "update-ref", "-d", "refs/remotes/origin/feat") // this clone never fetched it
	assert.NoError(t, preKillProof(pausedTarget(t, repo, gone), "w-x-1", gone),
		"the work is on the remote; a stale tracking cache must not refuse it")

	t.Run("a remote that cannot be reached is exit 3, not a stale-cache pass", func(t *testing.T) {
		gitq(t, repo, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))
		err := preKillProof(pausedTarget(t, repo, gone), "w-x-1", gone)
		require.Error(t, err)
		assert.Equal(t, exitCouldNotLook, exitCodeFor(err))
	})
}
