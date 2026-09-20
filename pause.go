package main

import (
	"claude-squad/config"
	"claude-squad/log"
	"claude-squad/session"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

// pauseInstance pauses one session the way the interface's Checkout key does:
// stops tmux, removes the worktree (keeps the branch), and persists the
// resulting Paused status to state -- so a later `resume` (or a restart of
// the interface) sees it.
//
// ⛔ SAME LOGGER-INIT AND LOAD PATTERN AS kill.go, FOR THE SAME REASON: session
// code logs through log.ErrorLog, nil until Initialize runs; Pause() needs a
// live *Instance with its worktree and tmux session attached, which only
// LoadInstances (not raw InstanceData) gives you.
func pauseInstance(title string) error {
	log.Initialize(false)
	defer log.Close()

	state := config.LoadState()
	storage, err := session.NewStorage(state)
	if err != nil {
		return couldNotLook("failed to open state: %v", err)
	}

	instances, err := storage.LoadInstances()
	if err != nil {
		return couldNotLook("failed to load instances: %v", err)
	}

	var target *session.Instance
	titles := make([]string, 0, len(instances))
	for _, inst := range instances {
		t := inst.ToInstanceData().Title
		titles = append(titles, t)
		if t == title {
			target = inst
		}
	}
	if target == nil {
		if len(titles) == 0 {
			return refused("no session named %q, and there are no sessions", title)
		}
		return refused("no session named %q. Sessions: %v", title, titles)
	}

	// ⛔ THE PRE-PAUSE PROOF (Rakizi/the-lab#30). Paused promises "resumable
	// from the kept branch". If the branch is not there that promise is a lie
	// and ⏸ is worse than nothing, so a missing branch is a refusal and a
	// check that could not run is a refusal (exit 3) -- never a pass. A branch
	// that exists but is reachable from no remote or tag is still pausable
	// (nothing is destroyed; the branch is kept), but the ticket's words are
	// "say plainly that resume may fail": the count goes to stderr so the
	// one place the work exists is named before the worktree is removed.
	wt, err := target.GetGitWorktree()
	if err != nil {
		return couldNotLook("no git worktree on %q: %v", title, err)
	}
	if err := prePauseProof(wt, title, os.Stderr); err != nil {
		return err
	}

	if err := target.Pause(); err != nil {
		return refused("failed to pause %q: %v", title, err)
	}

	// Close the Terminal-tab session, as the interface's Checkout key does via
	// CleanupTerminalForInstance. Pause removes the worktree, so a surviving
	// `term_<title>` session is sitting in a directory that no longer exists.
	closeTerminalSession(title)

	// ⛔ Pause() only mutates the in-memory Instance. Without this write the
	// status reverts to whatever was on disk the next time anything reads
	// state -- the same class of bug kill.go's own comment warns about for
	// DeleteInstance: a mutation that never reaches the file did not happen.
	if err := storage.SyncInstances(instances); err != nil {
		return fmt.Errorf(
			"%q was paused but its state entry could NOT be saved: %w\n"+
				"the on-disk status may still read running", title, err)
	}
	return nil
}

// branchProver is the slice of GitWorktree the pause proof needs, so the
// decision can be tested against a fake without a repository.
type branchProver interface {
	BranchExists() (bool, error)
	CommitsOnNoRemoteOrTag() (int, error)
	GetBranchName() string
}

// prePauseProof returns the reason NOT to pause, or nil. It writes the plain
// warning about an unreachable branch to w -- that is not a refusal.
func prePauseProof(wt branchProver, title string, w io.Writer) error {
	exists, err := wt.BranchExists()
	if err != nil {
		return couldNotLook("could not check that branch %s exists: %v", wt.GetBranchName(), err)
	}
	if !exists {
		return refused("branch %s does not exist, so a pause could never be resumed. Not pausing %q.",
			wt.GetBranchName(), title)
	}
	n, err := wt.CommitsOnNoRemoteOrTag()
	if err != nil {
		return couldNotLook("could not count commits on %s reachable from no remote or tag: %v",
			wt.GetBranchName(), err)
	}
	if n > 0 {
		fmt.Fprintf(w, "%s\t⚠ %d commit(s) on %s are on NO remote or tag. The local branch is the only "+
			"copy; if it is lost, resume will fail. Push or tag before relying on this pause.\n",
			title, n, wt.GetBranchName())
	}
	return nil
}

var pauseCmd = &cobra.Command{
	Use:   "pause <title>",
	Short: "Pause a session without opening the interface",
	Long: `Pause a session without opening the interface.

Stops the tmux session and removes the git worktree, but KEEPS the branch --
the same thing the interface's Checkout key does. Unlike kill, this is
recoverable: 'claude-squad resume <title>' recreates the worktree from the
kept branch and restarts (or reattaches) the tmux session.

  claude-squad pause my-task

Before the worktree is removed the branch is PROVED to exist (a pause whose
branch is gone could never be resumed, and is refused) and its commits are
counted against every remote and tag. Commits on no remote or tag do not block
the pause -- the branch is kept -- but are said plainly on stderr, because the
local branch is then the only copy and resume depends on it surviving.

Exit codes:

  0  paused
  1  bad arguments
  2  refused -- no such title, the branch does not exist, or the pause failed
     (e.g. already paused, dirty worktree that could not be committed)
  3  could not look -- state could not be read, or the branch could not be
     checked, so nothing was touched`,
	Args: cobra.ExactArgs(1),
	RunE: func(command *cobra.Command, args []string) error {
		title := args[0]
		if err := pauseInstance(title); err != nil {
			return err
		}
		fmt.Fprintf(command.OutOrStdout(), "%s\tpaused\n", title)
		return nil
	},
}
