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
