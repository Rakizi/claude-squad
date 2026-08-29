package main

import (
	cmd2 "claude-squad/cmd"
	"claude-squad/config"
	"claude-squad/daemon"
	"claude-squad/log"
	"claude-squad/session"
	"claude-squad/session/git"
	"claude-squad/session/tmux"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

var resetYes bool

// protectedBranchSet reads stored state for branches that EXISTED BEFORE their
// session, keyed by repo AND branch.
//
// ⛔ RESET MUST NOT DELETE A BRANCH IT DID NOT CREATE. Instance.Cleanup already
// refuses to (isExistingBranch, worktree_ops.go), and reset deleting the same
// branch that kill would spare is the two halves disagreeing again.
//
// ⚠ It reads RAW InstanceData -- the cheap read `ls` and `kill` use -- rather
// than LoadInstances, which restores every session and is a lot of machinery to
// obtain two strings per entry.
//
// A read failure returns an error, NOT an empty set. An empty set silently means
// "nothing is protected", which is the destructive answer.
func protectedBranchSet() (map[string]bool, int, error) {
	state := config.LoadState()
	var stored []session.InstanceData
	raw := state.GetInstances()
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &stored); err != nil {
			return nil, 0, couldNotLook("failed to read stored instances: %v", err)
		}
	}
	set := map[string]bool{}
	for _, d := range stored {
		if d.Worktree.IsExistingBranch && d.Worktree.BranchName != "" {
			set[git.ProtectedKey(d.Worktree.RepoPath, d.Worktree.BranchName)] = true
		}
	}
	return set, len(stored), nil
}

// renderPlan writes the counted confirm: WHAT will be deleted, grouped by the
// repository that owns it, and how much of it exists nowhere else.
//
// ⭐ THE POINT IS THE GROUPING. Printing a flat list of branches hides the defect
// this command had -- that the branches came from one repo and the directories
// from all of them. Grouped by repo, a reset reaching into a repo it should not
// is visible on the page before anything is destroyed.
func renderPlan(w io.Writer, plan *git.CleanupPlan, instances int) {
	byRepo := map[string][]git.CleanupTarget{}
	for _, t := range plan.Targets {
		byRepo[t.RepoPath] = append(byRepo[t.RepoPath], t)
	}

	branches := 0
	kept := 0
	for _, t := range plan.Targets {
		if t.Branch == "" {
			continue
		}
		if t.Protected {
			kept++
		} else {
			branches++
		}
	}

	fmt.Fprintf(w, "reset will destroy %d worktree(s) and %d branch(es) across %d repositor(ies).\n",
		len(plan.Targets), branches, len(plan.Repos()))
	fmt.Fprintf(w, "worktree root: %s\n", plan.Root)
	fmt.Fprintf(w, "state entries to be removed: %d\n\n", instances)

	repos := make([]string, 0, len(byRepo))
	for r := range byRepo {
		repos = append(repos, r)
	}
	sort.Strings(repos)

	for _, repo := range repos {
		fmt.Fprintf(w, "  %s\n", repo)
		targets := byRepo[repo]
		sort.Slice(targets, func(i, j int) bool { return targets[i].Path < targets[j].Path })
		for _, t := range targets {
			branch := t.Branch
			switch {
			case branch == "":
				branch = "(detached HEAD -- no branch)"
			case t.Protected:
				branch += "  [KEPT: pre-existing branch]"
			}
			note := ""
			switch {
			case t.Protected || t.Branch == "":
			case t.Unpushed < 0:
				note = "  ⛔ unpushed commits COULD NOT BE COUNTED"
			case t.Unpushed > 0:
				note = fmt.Sprintf("  ⛔ %d commit(s) on NO remote", t.Unpushed)
			}
			fmt.Fprintf(w, "    %-40s %s%s\n", branch, filepath.Base(t.Path), note)
		}
		fmt.Fprintln(w)
	}

	if len(plan.Orphans) > 0 {
		fmt.Fprintf(w, "  %d orphan director(ies) -- the DIRECTORY is removed, NO branch is touched:\n",
			len(plan.Orphans))
		for _, o := range plan.Orphans {
			fmt.Fprintf(w, "    %s  (%s)\n", o.Path, o.Why)
		}
		fmt.Fprintln(w)
	}

	if len(plan.NotExamined) > 0 {
		fmt.Fprintf(w, "  ⛔ %d director(ies) were NOT EXAMINED (deeper than %d levels). Nothing is done to them:\n",
			len(plan.NotExamined), 8)
		for _, d := range plan.NotExamined {
			fmt.Fprintf(w, "    %s\n", d)
		}
		fmt.Fprintln(w)
	}

	withWork, uncounted := plan.UnpushedTotal()
	if kept > 0 {
		fmt.Fprintf(w, "  %d branch(es) will be KEPT: they existed before their session.\n", kept)
	}
	if withWork > 0 {
		fmt.Fprintf(w, "  ⛔ %d branch(es) carry commits that are on NO remote. `git branch -D` is the end of them.\n", withWork)
	}
	if uncounted > 0 {
		fmt.Fprintf(w, "  ⛔ %d branch(es): unpushed work COULD NOT BE COUNTED. That is not the same as none.\n", uncounted)
	}
}

// resetCmd tears down every session: state entries, tmux sessions, worktrees and
// the branches those worktrees created.
//
// ⛔ THE ORDER CHANGED, AND IT MATTERS. The plan is computed and shown BEFORE
// storage is deleted, because storage is where "this branch existed before its
// session" is recorded. Deleting it first -- which is what this command used to
// do -- throws away the only thing that says which branches must be spared.
var resetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Remove every session: state, tmux, worktrees and their branches",
	Long: `Remove every session.

Without --yes this prints WHAT WOULD BE DESTROYED, grouped by the repository
that owns it, counted, and marked where a branch carries commits that are on no
remote -- then exits 2 without touching anything.

  claude-squad reset          # show the plan, destroy nothing
  claude-squad reset --yes    # do it

⛔ --yes is REQUIRED. There is no undo: branches go with git branch -D.

Each worktree directory is asked WHICH REPOSITORY OWNS IT, by running git inside
it, and every destructive git call is scoped to that repository. A directory no
repository claims loses only the DIRECTORY -- no branch is deleted on a guess.

Exit codes:

  0  reset completed
  1  bad arguments
  2  refused -- --yes was not given
  3  could not look -- state or the worktree root could not be read, so NOTHING
     was destroyed`,
	Args: cobra.NoArgs,
	RunE: func(command *cobra.Command, args []string) error {
		log.Initialize(false)
		defer log.Close()

		out := command.OutOrStdout()

		protected, instances, err := protectedBranchSet()
		if err != nil {
			return err
		}
		plan, err := git.PlanCleanup(protected)
		if err != nil {
			return couldNotLook("could not determine what reset would destroy: %v\n"+
				"NOTHING was destroyed.", err)
		}

		renderPlan(out, plan, instances)

		if !resetYes {
			return refused("refusing to reset without --yes. Nothing above was touched.")
		}

		state := config.LoadState()
		storage, err := session.NewStorage(state)
		if err != nil {
			return couldNotLook("failed to initialize storage: %v\nNOTHING was destroyed.", err)
		}
		if err := storage.DeleteAllInstances(); err != nil {
			return fmt.Errorf("failed to reset storage: %w", err)
		}
		fmt.Fprintln(out, "state entries removed")

		if err := tmux.CleanupSessions(cmd2.MakeExecutor()); err != nil {
			return fmt.Errorf("failed to cleanup tmux sessions: %w", err)
		}
		fmt.Fprintln(out, "tmux sessions cleaned up")

		res, err := git.ExecuteCleanup(plan)
		if err != nil {
			return fmt.Errorf("failed to clean up worktrees: %w", err)
		}
		fmt.Fprintf(out, "worktrees removed: %d · branches deleted: %d · branches kept: %d · orphan dirs removed: %d\n",
			res.WorktreesRemoved, res.BranchesDeleted, res.BranchesKept, res.OrphansRemoved)

		// ⛔ VERIFIED AT THE DESTINATION, NOT INFERRED FROM EXIT CODES. Anything
		// still present after the removal is named, and the command does not
		// claim success over it.
		if len(res.Failures) > 0 {
			return refused("reset did not finish. Still present:\n  %s",
				strings.Join(res.Failures, "\n  "))
		}

		if err := daemon.StopDaemon(); err != nil {
			return err
		}
		fmt.Fprintln(out, "daemon stopped")

		return nil
	},
}
