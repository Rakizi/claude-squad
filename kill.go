package main

import (
	"claude-squad/cmd"
	"claude-squad/config"
	"claude-squad/log"
	"claude-squad/session"
	"claude-squad/session/tmux"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var killYes bool

// killInstance removes one session the way the interface's D key does.
//
// ⛔ TWO CALLS, NOT ONE, AND THE ORDER MATTERS. Neither half is sufficient:
//
//	Instance.Kill()          closes the tmux session, then removes the git
//	                         worktree and its branch
//	Storage.DeleteInstance() removes the STATE ENTRY, and nothing else
//
// MEASURED 2026-08-26: a session reading only DeleteInstance assumed it removed
// all four and hand-edited state.json instead of using it. Delete the entry
// alone and the worktree, branch and tmux session keep existing with nothing
// listing them -- orphans that only `git worktree list` will ever show you.
// Kill alone and the entry survives pointing at nothing.
//
// tmux is closed BEFORE the worktree because the tmux session's working
// directory IS the worktree; removing it first leaves the session in a
// directory that no longer exists, which is the state that produced a screenful
// of "error capturing pane content" today.
func killInstance(command *cobra.Command, title string) error {
	// ⛔ WITHOUT THIS THE COMMAND PANICS. session code logs through log.ErrorLog,
	// which is nil until Initialize runs -- and the interface initialises it
	// while the CLI subcommands never did. MEASURED 2026-08-26: a nil-pointer
	// segfault mid-teardown, after the worktree was already gone.
	log.Initialize(false)
	defer log.Close()

	state := config.LoadState()
	storage, err := session.NewStorage(state)
	if err != nil {
		return couldNotLook("failed to open state: %v", err)
	}

	// ⚠ This restores every stored instance, because that is what LoadInstances
	// does -- FromInstanceData calls Start(false) per entry. `ls` deliberately
	// avoids it by reading raw InstanceData, but Kill() needs a live *Instance
	// with its worktree and tmux session attached, and there is no cheaper way
	// to get one. It is the same path the interface takes at startup.
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
	// Read the worktree path BEFORE Kill removes it.
	worktreePath := target.ToInstanceData().Worktree.WorktreePath

	// Tear down. If it fails, the entry is LEFT ALONE on purpose: an entry
	// pointing at a half-removed worktree is recoverable, an orphaned worktree
	// with no entry is the thing nobody finds.
	//
	// ⛔ BUT A FAILURE IS NOT AUTOMATICALLY A REASON TO STOP. `tmux kill-session`
	// exits 1 when the session is ALREADY GONE, and Close() returns that as an
	// error -- so a session that has already been torn down could never be
	// removed from state, which is precisely the stuck entry this command exists
	// to clear. MEASURED 2026-08-26 on exactly that entry.
	//
	// The question is not "did the teardown command succeed" but "is the thing
	// gone". So on error, ASK THE DESTINATIONS.
	if err := target.Kill(); err != nil {
		leftover, lookErr := stillPresent(title, worktreePath)
		if lookErr != nil {
			return couldNotLook(
				"tearing down %q failed (%v) and whether anything remains could NOT be\n"+
					"determined (%v). NOTHING was removed from state.", title, err, lookErr)
		}
		if len(leftover) > 0 {
			return refused("failed to tear down %q: %v\n  still present: %v", title, err, leftover)
		}
		fmt.Fprintf(command.OutOrStdout(), "%s\talready torn down (%v)\n", title, err)
	}
	// ⛔ NOT storage.DeleteInstance. It calls LoadInstances AGAIN -- and by now
	// the worktree is gone, so FromInstanceData's Start(false) errors, tries to
	// log, and (before the logging fix) died on a nil logger. MEASURED
	// 2026-08-26: it panicked here, leaving the session torn down with its entry
	// still listed.
	//
	// The entry is removed from RAW InstanceData instead, the way `ls` reads it:
	// no Start, no restore, no second traversal of a tree that is mid-removal.
	if err := removeStoredEntry(title); err != nil {
		return fmt.Errorf(
			"%q was torn down but its state entry could NOT be removed: %w\n"+
				"the entry now points at a worktree that no longer exists", title, err)
	}
	return nil
}

// stillPresent names what a teardown was supposed to remove and did not.
//
// It asks the DESTINATIONS rather than trusting the command's exit code -- the
// same reason every check today reads the thing back instead of the return
// value. An empty result means the session is gone however it got that way.
func stillPresent(title, worktreePath string) ([]string, error) {
	var left []string

	live, err := tmux.LiveSessions(cmd.MakeExecutor())
	if err != nil {
		// Whether the tmux session survives is UNKNOWN. Say so; do not guess.
		return nil, fmt.Errorf("could not list tmux sessions: %w", err)
	}
	want := tmux.SessionName(title)
	for _, name := range live {
		if name == want {
			left = append(left, "tmux session "+want)
		}
	}

	if worktreePath != "" {
		if _, err := os.Stat(worktreePath); err == nil {
			left = append(left, "worktree "+worktreePath)
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("could not stat %s: %w", worktreePath, err)
		}
	}
	return left, nil
}

// removeStoredEntry drops one title from stored state without restoring anything.
//
// ⚠ It re-reads the FILE first, so a session another writer added since this
// process started is carried through rather than erased -- the same hazard
// SyncInstances exists for, which a delete path is equally capable of causing.
func removeStoredEntry(title string) error {
	state := config.LoadState()
	var stored []session.InstanceData
	if raw := state.GetInstances(); len(raw) > 0 {
		if err := json.Unmarshal(raw, &stored); err != nil {
			return couldNotLook("failed to read stored instances: %v", err)
		}
	}
	kept := make([]session.InstanceData, 0, len(stored))
	for _, d := range stored {
		if d.Title != title {
			kept = append(kept, d)
		}
	}
	raw, err := json.Marshal(kept)
	if err != nil {
		return fmt.Errorf("failed to encode instances: %w", err)
	}
	return state.SaveInstances(raw)
}

var killCmd = &cobra.Command{
	Use:   "kill <title>",
	Short: "Remove a session without opening the interface",
	Long: `Remove a session without opening the interface.

Removes the tmux session, the git worktree, the branch and the state entry --
the same four things the interface's D key removes, by the same two calls.

  claude-squad kill my-task --yes

--yes is REQUIRED. The interface asks "Press y to confirm" before doing this,
and a command that does the same thing silently on a typed title is more
dangerous, not less. There is no undo: the branch goes with git branch -D.

⚠ A PAUSED session has no tmux session and no worktree by design, but it DOES
still have its branch, and that branch is the only place its work exists.
Killing it deletes that branch.

⛔ If the teardown fails the state entry is left alone deliberately. An entry
pointing at a half-removed worktree can be found and fixed; an orphaned
worktree with no entry is the one nobody ever finds.

Exit codes:

  0  removed
  1  bad arguments
  2  refused -- no such title, --yes not given, or the teardown failed
  3  could not look -- state could not be read, so NOTHING was removed`,
	Args: cobra.ExactArgs(1),
	RunE: func(command *cobra.Command, args []string) error {
		title := args[0]
		if !killYes {
			return refused(
				"refusing to remove %q without --yes.\n"+
					"This deletes the tmux session, the worktree, the branch and the\n"+
					"state entry. The branch goes with `git branch -D`. There is no undo.",
				title)
		}
		if err := killInstance(command, title); err != nil {
			return err
		}
		fmt.Printf("%s\tremoved\n", title)
		return nil
	},
}
