package git

import (
	"fmt"
	"os"
	"strings"
)

// Setup creates a new worktree for the session
func (g *GitWorktree) Setup() error {
	// Ensure worktrees directory exists early (can be done in parallel with branch check)
	worktreesDir, err := getWorktreeDirectory()
	if err != nil {
		return fmt.Errorf("failed to get worktree directory: %w", err)
	}

	if err := os.MkdirAll(worktreesDir, 0755); err != nil {
		return err
	}

	// If this worktree uses a pre-existing branch, always set up from that branch
	// (it may exist locally or only on the remote).
	if g.isExistingBranch {
		return g.setupFromExistingBranch()
	}

	// Check if branch exists using git CLI (much faster than go-git PlainOpen)
	_, err = g.runGitCommand(g.repoPath, "show-ref", "--verify", fmt.Sprintf("refs/heads/%s", g.branchName))
	if err == nil {
		return g.setupFromExistingBranch()
	}
	return g.setupNewWorktree()
}

// setupFromExistingBranch creates a worktree from an existing branch
func (g *GitWorktree) setupFromExistingBranch() error {
	// Directory already created in Setup(), skip duplicate creation

	// Clean up any existing worktree first
	_, _ = g.runGitCommand(g.repoPath, "worktree", "remove", "-f", g.worktreePath) // Ignore error if worktree doesn't exist
	// If the directory is still there (orphaned, not registered with git), drop it so `git worktree add` won't fail.
	_ = os.RemoveAll(g.worktreePath)

	// Check if the local branch exists
	_, localErr := g.runGitCommand(g.repoPath, "show-ref", "--verify", fmt.Sprintf("refs/heads/%s", g.branchName))
	if localErr != nil {
		// Local branch doesn't exist — check if remote tracking branch exists
		_, remoteErr := g.runGitCommand(g.repoPath, "show-ref", "--verify", fmt.Sprintf("refs/remotes/origin/%s", g.branchName))
		if remoteErr != nil {
			return fmt.Errorf("branch %s not found locally or on remote", g.branchName)
		}
		// Create a local tracking branch via worktree add -b
		if _, err := g.runGitCommand(g.repoPath, "worktree", "add", "-b", g.branchName, g.worktreePath, fmt.Sprintf("origin/%s", g.branchName)); err != nil {
			return fmt.Errorf("failed to create worktree from remote branch %s: %w", g.branchName, err)
		}
		return g.recordBaseCommit()
	}

	// Create a new worktree from the existing local branch
	if _, err := g.runGitCommand(g.repoPath, "worktree", "add", g.worktreePath, g.branchName); err != nil {
		return fmt.Errorf("failed to create worktree from branch %s: %w", g.branchName, err)
	}

	return g.recordBaseCommit()
}

// recordBaseCommit stores the commit the session starts from. Diffs are computed
// against it, so leaving it unset makes every git diff invocation fail with an
// ambiguous argument error once the session is running.
func (g *GitWorktree) recordBaseCommit() error {
	output, err := g.runGitCommand(g.worktreePath, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("failed to get base commit hash for branch %s: %w", g.branchName, err)
	}
	g.baseCommitSHA = strings.TrimSpace(output)
	return nil
}

// setupNewWorktree creates a new worktree from HEAD
func (g *GitWorktree) setupNewWorktree() error {
	// Clean up any existing worktree first
	_, _ = g.runGitCommand(g.repoPath, "worktree", "remove", "-f", g.worktreePath) // Ignore error if worktree doesn't exist
	// If the directory is still there (orphaned, not registered with git), drop it so `git worktree add` won't fail.
	_ = os.RemoveAll(g.worktreePath)

	// Clean up any existing branch using git CLI (much faster than go-git PlainOpen)
	_, _ = g.runGitCommand(g.repoPath, "branch", "-D", g.branchName) // Ignore error if branch doesn't exist

	// ⛔ THE BASE IS THE REMOTE DEFAULT BRANCH, NOT THE PARENT'S LOCAL HEAD.
	//
	// This resolves the TODO that stood here ("we might want to give an option
	// to use main/master instead of the current branch") and it is a CORRECTNESS
	// fix, not a preference.
	//
	// Reading the parent checkout's HEAD makes the shared checkout's transient
	// state the silent default base for every new session. MEASURED 2026-08-26 in
	// ~/the-lab/NextActionGuide: the reflog shows the shared checkout branch-
	// switched and committed on across two days, and for ~48 minutes its HEAD was
	// a feature branch. A session created in that window would have branched off
	// that feature branch and opened a plausible PR carrying someone else's
	// commits. Nothing was cut in that window -- timing, not protection.
	//
	// ⚠ AND A HUMAN IS NOT THE ONLY CALLER. ops/bin/dispatch launches workers on a
	// systemd timer, unattended. A person running `cs new` might notice the base
	// looked odd. A timer never will.
	baseRef, err := g.resolveBaseRef()
	if err != nil {
		return err
	}
	output, err := g.runGitCommand(g.repoPath, "rev-parse", baseRef)
	if err != nil {
		if strings.Contains(err.Error(), "fatal: ambiguous argument 'HEAD'") ||
			strings.Contains(err.Error(), "fatal: not a valid object name") ||
			strings.Contains(err.Error(), "fatal: HEAD: not a valid object name") {
			return fmt.Errorf("this appears to be a brand new repository: please create an initial commit before creating an instance")
		}
		return fmt.Errorf("failed to resolve base ref %s: %w", baseRef, err)
	}
	headCommit := strings.TrimSpace(string(output))
	g.baseCommitSHA = headCommit

	// Create a new worktree from the resolved base commit, so it starts clean and
	// does not inherit the parent worktree's uncommitted changes.
	if _, err := g.runGitCommand(g.repoPath, "worktree", "add", "-b", g.branchName, g.worktreePath, headCommit); err != nil {
		return fmt.Errorf("failed to create worktree from %s (%s): %w", baseRef, headCommit, err)
	}

	return nil
}

// Cleanup removes the worktree and associated branch
func (g *GitWorktree) Cleanup() error {
	var errs []error

	// Check if worktree path exists before attempting removal
	if _, err := os.Stat(g.worktreePath); err == nil {
		// Remove the worktree using git command
		if _, err := g.runGitCommand(g.repoPath, "worktree", "remove", "-f", g.worktreePath); err != nil {
			errs = append(errs, err)
		}
	} else if !os.IsNotExist(err) {
		// Only append error if it's not a "not exists" error
		errs = append(errs, fmt.Errorf("failed to check worktree path: %w", err))
	}

	// Delete the branch using git CLI, but skip if this is a pre-existing branch
	if !g.isExistingBranch {
		if _, err := g.runGitCommand(g.repoPath, "branch", "-D", g.branchName); err != nil {
			// Only log if it's not a "branch not found" error
			if !strings.Contains(err.Error(), "not found") {
				errs = append(errs, fmt.Errorf("failed to remove branch %s: %w", g.branchName, err))
			}
		}
	}

	// Prune the worktree to clean up any remaining references
	if err := g.Prune(); err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return g.combineErrors(errs)
	}

	return nil
}

// Remove removes the worktree but keeps the branch
func (g *GitWorktree) Remove() error {
	// Remove the worktree using git command
	if _, err := g.runGitCommand(g.repoPath, "worktree", "remove", "-f", g.worktreePath); err != nil {
		return fmt.Errorf("failed to remove worktree: %w", err)
	}

	return nil
}

// Prune removes all working tree administrative files and directories
func (g *GitWorktree) Prune() error {
	if _, err := g.runGitCommand(g.repoPath, "worktree", "prune"); err != nil {
		return fmt.Errorf("failed to prune worktrees: %w", err)
	}
	return nil
}

// resolveBaseRef returns the ref a new session's worktree should be cut from.
//
// ⭐ THREE OUTCOMES, NOT TWO -- and the third is why this is a function and not
// one inline call. "the remote default branch" / "there is no remote, so the
// local HEAD is genuinely the only base there is" / "there IS a remote but we
// could not read it" are different situations, and the last one must not
// silently render as the second. A repo that HAS an origin whose HEAD we cannot
// read is exactly the case where falling back to local HEAD reintroduces the
// defect this function exists to remove -- so it is an error, not a fallback.
//
// CLAUDE_SQUAD_BASE_REF overrides everything, for the deliberate case of cutting
// a session from somewhere specific.
func (g *GitWorktree) resolveBaseRef() (string, error) {
	if ref := strings.TrimSpace(os.Getenv("CLAUDE_SQUAD_BASE_REF")); ref != "" {
		return ref, nil
	}

	remotes, err := g.runGitCommand(g.repoPath, "remote")
	if err != nil {
		return "", fmt.Errorf("could not determine whether this repo has a remote: %w", err)
	}
	if strings.TrimSpace(string(remotes)) == "" {
		// No remote at all. Local HEAD is not a compromise here -- it is the only
		// base that exists, and there is no shared branch for it to drift from.
		return "HEAD", nil
	}

	// ⛔ THE REMOTE COUNTERPART OF THE BRANCH THIS CHECKOUT IS ON COMES FIRST,
	//    BEFORE origin/HEAD. Found the hard way: NextActionGuide works on
	//    `develop`, but its origin/HEAD points at `main`, and develop is 2,500
	//    commits AHEAD of main. Preferring origin/HEAD cut every new session from
	//    a base 2,500 commits stale -- far worse than the defect this function
	//    was written to fix.
	//
	// ⭐ THE SPLIT THAT MAKES THIS SAFE: take the branch NAME from local HEAD,
	//    take the COMMIT from the remote. A name cannot carry uncommitted work or
	//    unpushed local commits, so the parent checkout's dirty state still
	//    cannot leak into a new session -- which was the whole point.
	if out, err := g.runGitCommand(g.repoPath, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		cur := strings.TrimSpace(string(out))
		if cur != "" && cur != "HEAD" { // "HEAD" means detached: no branch name to use
			remoteRef := "origin/" + cur
			if _, err := g.runGitCommand(g.repoPath, "rev-parse", "--verify", remoteRef); err == nil {
				return remoteRef, nil
			}
		}
	}

	// origin/HEAD points at the remote's default branch when it has been set.
	if out, err := g.runGitCommand(g.repoPath, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if ref := strings.TrimSpace(string(out)); ref != "" {
			return ref, nil
		}
	}

	// Not set locally -- ask the remote itself, then fall back to the usual names.
	if out, err := g.runGitCommand(g.repoPath, "ls-remote", "--symref", "origin", "HEAD"); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "ref: refs/heads/") {
				name := strings.TrimSpace(strings.TrimPrefix(strings.Fields(line)[1], "refs/heads/"))
				if name != "" {
					return "origin/" + name, nil
				}
			}
		}
	}
	for _, name := range []string{"origin/develop", "origin/main", "origin/master"} {
		if _, err := g.runGitCommand(g.repoPath, "rev-parse", "--verify", name); err == nil {
			return name, nil
		}
	}

	// ⛔ COULD NOT LOOK. There is a remote, but nothing we tried resolved. Do NOT
	// fall back to local HEAD -- that is the exact defect, and a silent fallback
	// would make it invisible again.
	return "", fmt.Errorf("repo has a remote but no default branch could be resolved; " +
		"set CLAUDE_SQUAD_BASE_REF to choose one explicitly (tried origin/HEAD, " +
		"ls-remote, origin/develop, origin/main, origin/master)")
}
