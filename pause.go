package main

import (
	"claude-squad/config"
	"claude-squad/log"
	"claude-squad/session"
	"fmt"

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

	if err := target.Pause(); err != nil {
		return refused("failed to pause %q: %v", title, err)
	}

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

var pauseCmd = &cobra.Command{
	Use:   "pause <title>",
	Short: "Pause a session without opening the interface",
	Long: `Pause a session without opening the interface.

Stops the tmux session and removes the git worktree, but KEEPS the branch --
the same thing the interface's Checkout key does. Unlike kill, this is
recoverable: 'claude-squad resume <title>' recreates the worktree from the
kept branch and restarts (or reattaches) the tmux session.

  claude-squad pause my-task

Exit codes:

  0  paused
  1  bad arguments
  2  refused -- no such title, or the pause failed (e.g. already paused,
     dirty worktree that could not be committed)
  3  could not look -- state could not be read, so nothing was touched`,
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
