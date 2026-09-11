package main

import (
	"claude-squad/log"
	"claude-squad/session"
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
}

func TestJudgeLocalOnly(t *testing.T) {
	assert.NoError(t, judgeLocalOnly(0, nil), "clean branch proceeds")

	err := judgeLocalOnly(1, nil)
	require.Error(t, err, "one unpushed commit MUST refuse")
	assert.Equal(t, exitRefused, exitCodeFor(err))

	err = judgeLocalOnly(0, errors.New("not a git repository"))
	require.Error(t, err, "a count that could not run MUST refuse, even though n is 0")
	assert.Equal(t, exitCouldNotLook, exitCodeFor(err))
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

func gitq(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
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
}
