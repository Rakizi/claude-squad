package main

import (
	"claude-squad/config"
	"claude-squad/log"
	"claude-squad/session"
	"fmt"

	"github.com/spf13/cobra"
)

// resumeInstance resumes one dormant session -- Paused on purpose, or Unknown
// because its tmux session vanished -- the way the interface's Resume key
// does: recreates the worktree from the kept branch if it is gone, restarts
// (or reattaches) the tmux session, and persists the resulting Running status.
func resumeInstance(title string) error {
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

	if err := target.Resume(); err != nil {
		return refused("failed to resume %q: %v", title, err)
	}

	// ⛔ SAME REASON AS pause.go: Resume() only mutates the in-memory
	// Instance. Without this the running status never reaches state.json.
	if err := storage.SyncInstances(instances); err != nil {
		return fmt.Errorf(
			"%q was resumed but its state entry could NOT be saved: %w\n"+
				"the on-disk status may still read paused", title, err)
	}
	return nil
}

var resumeCmd = &cobra.Command{
	Use:   "resume <title>",
	Short: "Resume a paused or unknown session without opening the interface",
	Long: `Resume a paused or unknown session without opening the interface.

Recreates the git worktree from the branch a prior 'claude-squad pause' kept
(or leaves it alone if it is still on disk), and restarts (or reattaches, if
the tmux session somehow survived) the session -- the same thing the
interface's Resume key does. Works on a session that is Paused, and on one
whose status is Unknown because its tmux session vanished (server died,
killed, crashed) -- that is the recovery path for it. Resuming a running
session is refused.

  claude-squad resume my-task

Exit codes:

  0  resumed
  1  bad arguments
  2  refused -- no such title, neither paused nor unknown, or the resume
     failed (e.g. branch is checked out elsewhere and must be switched away
     from first)
  3  could not look -- state could not be read, so nothing was touched`,
	Args: cobra.ExactArgs(1),
	RunE: func(command *cobra.Command, args []string) error {
		title := args[0]
		if err := resumeInstance(title); err != nil {
			return err
		}
		fmt.Fprintf(command.OutOrStdout(), "%s\tresumed\n", title)
		return nil
	},
}
