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

var (
	killYes   bool
	killForce bool
)

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
//
// Returns the note to print after "removed": empty normally, the recorded
// override when --force skipped a refusal.
func killInstance(command *cobra.Command, title string, force bool) (string, error) {
	// ⛔ WITHOUT THIS THE COMMAND PANICS. session code logs through log.ErrorLog,
	// which is nil until Initialize runs -- and the interface initialises it
	// while the CLI subcommands never did. MEASURED 2026-08-26: a nil-pointer
	// segfault mid-teardown, after the worktree was already gone.
	log.Initialize(false)
	defer log.Close()

	state := config.LoadState()
	storage, err := session.NewStorage(state)
	if err != nil {
		return "", couldNotLook("failed to open state: %v", err)
	}

	// ⚠ This restores every stored instance, because that is what LoadInstances
	// does -- FromInstanceData calls Start(false) per entry. `ls` deliberately
	// avoids it by reading raw InstanceData, but Kill() needs a live *Instance
	// with its worktree and tmux session attached, and there is no cheaper way
	// to get one. It is the same path the interface takes at startup.
	instances, err := storage.LoadInstances()
	if err != nil {
		return "", couldNotLook("failed to load instances: %v", err)
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
			return "", refused("no session named %q, and there are no sessions", title)
		}
		return "", refused("no session named %q. Sessions: %v", title, titles)
	}
	// Read the worktree path, repo and branch BEFORE Kill removes them.
	wt := target.ToInstanceData().Worktree
	worktreePath := wt.WorktreePath
	branchName := wt.BranchName

	// The branch probe, captured BEFORE Kill: stillPresent asks it whether the
	// branch survived a failed teardown.
	//
	// ⛔ ONLY WHEN THE BRANCH IS ACTUALLY A DESTINATION. session/git's Cleanup
	// deletes the branch `if !g.isExistingBranch` -- a session made by
	// `cs new --branch <existing>` KEEPS its branch by design. Counting that
	// branch as a leftover refuses the kill forever, and --force does not reach
	// here (it covers preKillProof only). MEASURED 2026-09-20: an
	// existing-branch session whose tmux server had died went from `rc=0, entry
	// cleared` to `rc=2` on every retry, permanently stuck -- the exact stuck
	// entry this command exists to clear. Control in the same run: an ordinary
	// paused session still cleared, so the bug was specific to this flag.
	//
	// ⛔ AND `var probe branchProbe` IS NOT COSMETIC. Assigning a nil
	// *git.GitWorktree to the interface makes a NON-NIL interface holding a nil
	// pointer, so `probe != nil` is true and BranchExists dereferences it.
	// Measured: panic. A nil interface is the only spelling that skips the leg.
	var probe branchProbe
	gw, gwErr := target.GetGitWorktree()
	switch {
	case gwErr != nil:
		log.WarningLog.Printf("could not read worktree for %q; branch leftovers will not be checked: %v", title, gwErr)
	case wt.IsExistingBranch:
		log.InfoLog.Printf("%q runs on a pre-existing branch; the teardown keeps it, so it is not a leftover", title)
	default:
		probe = gw
	}

	// ⛔ THE PRE-KILL PROOF, before anything is torn down. See prekill.go. A
	// refusal here has removed NOTHING. --force does not skip the check; it
	// skips the refusal and records what it stepped over.
	var note string
	if err := preKillProof(target, title, worktreePath); err != nil {
		if !force {
			return "", err
		}
		note = recordForcedKill(title, err)
	}

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
		leftover, lookErr := stillPresent(title, worktreePath, probe, branchName)
		if lookErr != nil {
			return "", couldNotLook(
				"tearing down %q failed (%v) and whether anything remains could NOT be\n"+
					"determined (%v). NOTHING was removed from state.", title, err, lookErr)
		}
		if len(leftover) > 0 {
			return "", refused("failed to tear down %q: %v\n  still present: %v", title, err, leftover)
		}
		fmt.Fprintf(command.OutOrStdout(), "%s\talready torn down (%v)\n", title, err)
	}

	// Close the Terminal-tab session, as the interface's D does via
	// CleanupTerminalForInstance. It is a SEPARATE tmux session (`term_<title>`)
	// that Instance.Kill knows nothing about, and once the state entry is gone
	// nothing will ever reap it. MEASURED 2026-09-20: `cs kill` left
	// claudesquad_term_cslab-k3 running with its instance already deleted.
	//
	// ⛔ ONLY ONCE THE SESSION IS PROVABLY GONE. Every `return` above leaves the
	// pane untouched, which is what "a refused kill removed NOTHING" has to mean
	// -- and an earlier revision of this file put the call before the teardown,
	// where a post-Kill refusal (exit 2) had already closed a pane the caller
	// was told was untouched.
	closeTerminalSession(title)
	// ⛔ NOT storage.DeleteInstance. It calls LoadInstances AGAIN -- and by now
	// the worktree is gone, so FromInstanceData's Start(false) errors, tries to
	// log, and (before the logging fix) died on a nil logger. MEASURED
	// 2026-08-26: it panicked here, leaving the session torn down with its entry
	// still listed.
	//
	// The entry is removed from RAW InstanceData instead, the way `ls` reads it:
	// no Start, no restore, no second traversal of a tree that is mid-removal.
	if err := removeStoredEntry(title); err != nil {
		return "", fmt.Errorf(
			"%q was torn down but its state entry could NOT be removed: %w\n"+
				"the entry now points at a worktree that no longer exists", title, err)
	}
	return note, nil
}

// closeTerminalSession closes the Terminal-tab tmux session for a title, if one
// exists. Best-effort by design: a session that was never opened is not an
// error, and failing to close it must not stop a teardown that otherwise
// succeeded.
//
// ⚠ The two seams exist so a test can observe the call. Without them nothing
// detected this function being DELETED or HOISTED in front of the pre-kill
// proof -- both mutants compiled and the whole suite stayed green, which is a
// test suite reporting protection it does not provide.
var (
	listTmuxSessions = func() ([]string, error) { return tmux.LiveSessions(cmd.MakeExecutor()) }
	closeTmuxSession = func(name string) error { return tmux.NewTmuxSession(name, "").Close() }
)

func closeTerminalSession(title string) {
	want := tmux.SessionName("term_" + title)
	live, err := listTmuxSessions()
	if err != nil {
		// COULD NOT LOOK. Say so in the log rather than guessing it is absent.
		log.WarningLog.Printf("could not list tmux sessions to close %s: %v", want, err)
		return
	}
	for _, name := range live {
		if name == want {
			if err := closeTmuxSession("term_" + title); err != nil {
				log.WarningLog.Printf("failed to close terminal session %s: %v", want, err)
			}
			return
		}
	}
}

// stillPresent reports which of a session's destinations survived a failed
// teardown. ⛔ THE BRANCH IS A DESTINATION TOO -- WHEN THE TEARDOWN WOULD HAVE
// DELETED IT; an existing-branch session keeps its branch and the caller passes
// no probe for it. This function checked the
// tmux session and the worktree PATH only, and a branch that could not be
// deleted left both of those clean -- so `cs kill` printed the branch error,
// concluded "already torn down", removed the state entry and EXITED 0.
//
// MEASURED 2026-09-20: pause a session, check its branch out in the main repo
// (which is exactly what pause invites you to do -- it copies the branch name
// to the clipboard), then kill it:
//
//	cslab-k2  already torn down (failed to cleanup git worktree: ... cannot
//	          delete branch 'rakizi/cslab-k2' used by worktree at ...)
//	cslab-k2  removed
//	kill rc=0                    <- and `git branch` still lists it
//
// The interface refuses that kill up front on IsBranchCheckedOut. This is the
// same refusal reached from the other end: ask the destinations, and count the
// branch among them.
// branchProbe is BranchExists narrowed to what stillPresent needs, so the
// branch leg can be driven in a test without building a repository.
type branchProbe interface {
	BranchExists() (bool, error)
}

func stillPresent(title, worktreePath string, branch branchProbe, branchName string) ([]string, error) {
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

	// ⛔ session/git ALREADY OWNS THIS QUESTION -- (*GitWorktree).BranchExists,
	// added by the pre-kill proof, with its own three-state test. This file
	// carried a second copy for an afternoon; a second implementation of one
	// concept is how the two drift apart. MEASURED while removing it: the
	// duplicate used `show-ref --verify` WITHOUT --quiet, which exits 128 for a
	// missing ref as well as for an unreadable repo -- it could not tell absent
	// from could-not-look at all. `--quiet` is what makes the three codes
	// distinct (0 / 1 / 128), and session/git had it right.
	if branch != nil && branchName != "" {
		exists, err := branch.BranchExists()
		if err != nil {
			return nil, err
		}
		if exists {
			left = append(left, "branch "+branchName)
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

⛔ --yes means "do not ask me". It does not mean "do not check". Before the
teardown, two measurements must come back clean or the kill is REFUSED with
nothing removed:

  · the branch's commits reachable from no remote and no tag
    (git rev-list --not --remotes --tags, after refreshing the remote-tracking
    ref) must be 0 -- a tag is as good a harbour as a push, and a count or a
    refresh that could not run is a refusal, not a zero
  · agent-trace <title> --json must not report LOCAL_ONLY_WORK (exit 2) or
    CANNOT_TELL (exit 3); a tool that is missing, hangs or prints no JSON is
    exit 3. Its other states (LANDED, UNFINISHED, DECISION_UNRELAYED) proceed.
    Skipped, with the refs count standing alone, when the worktree is already
    gone from disk (a paused session) -- there is nothing for it to read.

--force steps over a refusal. It still runs the checks, and RECORDS what it
stepped over: on stdout after "removed", on stderr, and in the log file.

⚠ A PAUSED session has no tmux session and no worktree by design, but it DOES
still have its branch, and that branch is the only place its work exists.
Killing it deletes that branch.

⛔ If the teardown fails the state entry is left alone deliberately. An entry
pointing at a half-removed worktree can be found and fixed; an orphaned
worktree with no entry is the one nobody ever finds.

Exit codes:

  0  removed
  1  bad arguments
  2  refused -- no such title, --yes not given, the pre-kill proof found
     local-only commits, or the teardown failed
  3  could not look -- state could not be read, or the pre-kill proof could
     not run. NOTHING was removed`,
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
		note, err := killInstance(command, title, killForce)
		if err != nil {
			return err
		}
		if note != "" {
			fmt.Printf("%s\tremoved\t%s\n", title, note)
		} else {
			fmt.Printf("%s\tremoved\n", title)
		}
		return nil
	},
}
