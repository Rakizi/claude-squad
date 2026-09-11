package git

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// BranchExists reports whether refs/heads/<branch> exists in the repository.
//
// Three outcomes, kept apart on purpose: (true, nil) it is there; (false, nil)
// it is NOT there -- `show-ref --verify` exited 1; (false, err) the question
// could not be answered (not a repository, unreadable, exit 128). A caller
// deciding whether a pause or kill is safe must treat the third as a refusal,
// never as an absence (Rakizi/the-lab#30).
func (g *GitWorktree) BranchExists() (bool, error) {
	_, err := g.runGitCommand(g.repoPath, "show-ref", "--verify", "--quiet", "refs/heads/"+g.branchName)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("could not check branch %s in %s: %w", g.branchName, g.repoPath, err)
}

// LocalOnlyCommits counts the commits reachable from the session branch that
// are reachable from NO remote-tracking ref -- the commits a `git branch -D`
// would destroy. It is computed in the main repository from refs, so it works
// for a paused session whose worktree is gone.
//
// A repository with no remotes at all reports every commit on the branch,
// which is the honest answer: nothing exists anywhere else.
//
// (0, nil) is "counted, none"; (n, err) with err != nil is "could not count" --
// a missing branch, an unreadable repository -- and n is meaningless then.
func (g *GitWorktree) LocalOnlyCommits() (int, error) {
	return g.commitsNotReachableFrom("--remotes")
}

// CommitsOnNoRemoteOrTag is LocalOnlyCommits with tags also counted as a safe
// harbour: a tagged commit survives the branch being deleted. This is the
// "reachable from a remote or a tag" proof a pause runs before tearing the
// worktree down.
func (g *GitWorktree) CommitsOnNoRemoteOrTag() (int, error) {
	return g.commitsNotReachableFrom("--remotes", "--tags")
}

// commitsNotReachableFrom runs `git rev-list --count refs/heads/<branch> --not
// <excludes...>`. After --not, --remotes and --tags act as negated revision
// groups, so the count is "on the branch but on none of those".
func (g *GitWorktree) commitsNotReachableFrom(excludes ...string) (int, error) {
	args := append([]string{"rev-list", "--count", "refs/heads/" + g.branchName, "--not"}, excludes...)
	out, err := g.runGitCommand(g.repoPath, args...)
	if err != nil {
		return 0, fmt.Errorf("could not count commits on %s not reachable from %s: %w",
			g.branchName, strings.Join(excludes, " "), err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("rev-list --count printed %q, not a number", strings.TrimSpace(out))
	}
	return n, nil
}
