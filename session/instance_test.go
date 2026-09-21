package session

import (
	"claude-squad/cmd/cmd_test"
	"claude-squad/log"
	"claude-squad/session/tmux"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	log.Initialize(false)
	defer log.Close()
	os.Exit(m.Run())
}

// nullPtyFactory hands back a throwaway file instead of a real PTY.
type nullPtyFactory struct {
	t     *testing.T
	calls int
}

func (p *nullPtyFactory) Start(cmd *exec.Cmd) (*os.File, error) {
	p.calls++
	return os.OpenFile(filepath.Join(p.t.TempDir(), "pty"), os.O_CREATE|os.O_RDWR, 0644)
}

func (p *nullPtyFactory) Close() {}

// When the tmux server dies between runs, every session goes with it while the worktree
// and branch survive on disk. Restoring such an instance must mark it Unknown -- NOT
// Paused -- so the user can resume it without it masquerading as a deliberate pause.
// Returning an error instead is not an option: LoadInstances aborts on the first
// failure, so one dead session would hide every other instance.
// See https://github.com/smtg-ai/claude-squad/issues/216 and Rakizi/the-lab#30.
func TestStartMarksInstanceUnknownWhenTmuxSessionNoLongerExists(t *testing.T) {
	ptyFactory := &nullPtyFactory{t: t}
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error {
			if strings.Contains(cmd.String(), "has-session") {
				return fmt.Errorf("can't find session")
			}
			return nil
		},
	}

	instance, err := NewInstance(InstanceOptions{Title: "revived", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	instance.SetTmuxSession(tmux.NewTmuxSessionWithDeps("revived", "claude", ptyFactory, cmdExec))

	require.NoError(t, instance.Start(false), "a dead tmux session is recoverable, not a startup failure")
	require.Equal(t, Unknown, instance.Status)
	// ⛔ THE DISCRIMINATION. Paused means "torn down on purpose"; a vanished tmux
	// session is not that, and rendering it as ⏸ is how 23 dead sessions hid.
	require.NotEqual(t, Paused, instance.Status, "a vanished tmux session must not read as a deliberate pause")
	require.True(t, instance.Dormant(), "Unknown has no tmux session; every tmux guard must cover it")
	require.False(t, instance.Paused())
	require.True(t, instance.Started())
	require.Zero(t, ptyFactory.calls, "should not attach to a session that does not exist")
}

// The control for the test above: a deliberately Paused instance is loaded as
// Paused and is NOT re-measured into Unknown -- the two states must stay apart in
// both directions.
func TestPausedInstanceStaysPausedOnLoad(t *testing.T) {
	data := InstanceData{Title: "parked", Status: Paused, Program: "claude"}
	instance, err := FromInstanceData(data)
	require.NoError(t, err)
	require.Equal(t, Paused, instance.Status)
	require.True(t, instance.Dormant())
	require.False(t, instance.Unknown())
}

// The happy path is unchanged: an instance whose session survived comes back Running.
func TestStartRestoresInstanceWhenTmuxSessionSurvives(t *testing.T) {
	ptyFactory := &nullPtyFactory{t: t}
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error { return nil },
	}

	instance, err := NewInstance(InstanceOptions{Title: "alive", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	instance.SetTmuxSession(tmux.NewTmuxSessionWithDeps("alive", "claude", ptyFactory, cmdExec))

	require.NoError(t, instance.Start(false))
	require.Equal(t, Running, instance.Status)
	require.Equal(t, 1, ptyFactory.calls)
}

// Resume must accept an Unknown instance: that is the recovery path for a
// session whose tmux vanished, and it is the mutation both reviewers found
// green -- `if i.Status != Paused` in Resume() left the suite passing.
func TestResumeAcceptsUnknown(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	gitq(t, root, "init", "-q", "-b", "main", repo)
	gitq(t, repo, "config", "user.email", "t@t")
	gitq(t, repo, "config", "user.name", "t")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "a"), []byte("a\n"), 0644))
	gitq(t, repo, "add", "a")
	gitq(t, repo, "commit", "-qm", "base")
	gitq(t, repo, "branch", "feat")
	worktree := filepath.Join(root, "wt")
	gitq(t, repo, "worktree", "add", "-q", worktree, "feat") // still on disk: the tmux-died shape

	build := func(status Status) *Instance {
		// Paused loads without touching tmux; then the mocked session and the
		// status under test are set, so only Resume's gate differs between
		// the two cases.
		inst, err := FromInstanceData(InstanceData{
			Title: "vanished", Status: Paused, Program: "claude", Branch: "feat",
			Worktree: GitWorktreeData{RepoPath: repo, WorktreePath: worktree, BranchName: "feat", SessionName: "vanished"},
		})
		require.NoError(t, err)
		inst.SetTmuxSession(tmux.NewTmuxSessionWithDeps("vanished", "claude", &nullPtyFactory{t: t},
			cmd_test.MockCmdExec{RunFunc: func(*exec.Cmd) error { return nil }}))
		inst.SetStatus(status)
		return inst
	}

	inst := build(Unknown)
	require.NoError(t, inst.Resume(), "Unknown is resumable: the worktree is on disk and tmux is rebuilt")
	require.Equal(t, Running, inst.Status)

	// Control: the gate still refuses a Running instance.
	require.Error(t, build(Running).Resume())
}

func gitq(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}

// ⛔ PAUSED MEANS RESUMABLE, AND NOTHING PROVED IT. Pause tears the worktree
// down; the BRANCH is the only thing a Resume can rebuild from. If it is gone,
// marking the instance Paused -- a state whose whole contract is "torn down on
// purpose, resumable" -- claims recoverability over work that cannot be
// recovered. Rakizi/the-lab#30.
//
// ⭐ DRIVEN AGAINST REAL GIT, not a stub: the question is what `git
// show-ref`/`rev-parse` actually says, and a fake that returns the expected
// answer would pass while the real command was wrong.
func pausableInstance(t *testing.T, branch string) *Instance {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	require.NoError(t, os.MkdirAll(repo, 0o755))
	gitq(t, repo, "init", "-q")
	gitq(t, repo, "config", "user.email", "t@t")
	gitq(t, repo, "config", "user.name", "t")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "f.txt"), []byte("a"), 0o644))
	gitq(t, repo, "add", "f.txt")
	gitq(t, repo, "commit", "-q", "-m", "base")
	gitq(t, repo, "branch", branch)
	worktree := filepath.Join(root, "wt")
	gitq(t, repo, "worktree", "add", "-q", worktree, branch)

	inst, err := FromInstanceData(InstanceData{
		Title: "parked", Status: Running, Program: "claude", Branch: branch,
		Worktree: GitWorktreeData{RepoPath: repo, WorktreePath: worktree,
			BranchName: branch, SessionName: "parked"},
	})
	require.NoError(t, err)
	inst.SetTmuxSession(tmux.NewTmuxSessionWithDeps("parked", "claude", &nullPtyFactory{t: t},
		cmd_test.MockCmdExec{RunFunc: func(*exec.Cmd) error { return nil }}))
	return inst
}

func TestPauseIsUnknownWhenTheBranchIsGone(t *testing.T) {
	inst := pausableInstance(t, "feat")
	// remove the worktree first so git will let the branch go, then delete it:
	// exactly the state a pause would be entering if the branch had vanished
	gitq(t, inst.gitWorktree.GetRepoPath(), "worktree", "remove", "--force",
		inst.gitWorktree.GetWorktreePath())
	gitq(t, inst.gitWorktree.GetRepoPath(), "branch", "-D", "feat")

	err := inst.Pause()
	require.Error(t, err, "a pause that cannot be resumed is not a success")
	require.Equal(t, Unknown, inst.Status)
	// ⛔ THE DISCRIMINATION, in the direction that matters: Paused would tell
	// the operator, and `cs ls`, that this comes back. It does not.
	require.NotEqual(t, Paused, inst.Status,
		"a torn-down instance whose branch is gone must not read as resumable")
}

// ⭐ THE CONTROL. Without it the test above passes for a Pause that can never
// reach Paused at all -- a blanket refusal wearing the shape of a safety check.
func TestPauseStillReachesPausedWhenTheBranchSurvives(t *testing.T) {
	inst := pausableInstance(t, "feat")
	require.NoError(t, inst.Pause())
	require.Equal(t, Paused, inst.Status)
	require.False(t, inst.Unknown())
}
