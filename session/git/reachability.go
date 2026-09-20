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

// CommitsOnNoRemoteOrTag counts the commits reachable from the session branch
// that are reachable from NO remote-tracking ref and NO tag -- exactly the
// commits `git branch -D` would make unreachable. A tagged commit survives the
// branch being deleted, so a tag is as good a harbour as a push here, and the
// refusal that prints "push or tag them first" is cleared by either.
//
// It is computed in the main repository from refs, so it works for a paused
// session whose worktree is gone. A repository with no remotes reports every
// untagged commit on the branch, which is the honest answer: nothing exists
// anywhere else.
//
// ⚠ It reads the LOCAL VIEW of the remotes. Remote-tracking refs are a cache:
// a branch pushed from another clone, or deleted on the remote since the last
// fetch, is misreported until something fetches. A caller about to act on the
// count calls RefreshRemoteBranch first; `ls`, which must not modify anything,
// reads the cache and says so.
//
// (0, nil) is "counted, none"; (n, err) with err != nil is "could not count" --
// a missing branch, an unreadable repository -- and n is meaningless then.
func (g *GitWorktree) CommitsOnNoRemoteOrTag() (int, error) {
	args := []string{"rev-list", "--count", "refs/heads/" + g.branchName, "--not", "--remotes", "--tags"}
	out, err := g.runGitCommand(g.repoPath, args...)
	if err != nil {
		return 0, fmt.Errorf("could not count commits on %s not reachable from any remote or tag: %w",
			g.branchName, err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("rev-list --count printed %q, not a number", strings.TrimSpace(out))
	}
	return n, nil
}

// RefreshRemoteBranch brings every remote's tracking ref for the session
// branch up to date with the remote itself, so that a count taken afterwards
// measures the remote and not a stale cache. Measured on this estate (PR #3
// review): a branch pushed and present on the remote counted as 2 local-only
// commits because this clone had never fetched it; one fetch later, 0.
//
// Per remote it runs
//
//	git fetch <remote> +refs/heads/<b>:refs/remotes/<remote>/<b>
//
// which (measured) updates the tracking ref when the remote has the branch.
// When the remote no longer has it, that exits 128 with "couldn't find remote
// ref" -- a measured absence, not a failure. ⚠ MEASURED: `--prune` does NOT
// drop the stale tracking ref in that scoped form (only a whole-remote fetch
// does, and NextActionGuide has ~600 remote heads to pull per kill), so the
// stale ref is deleted here directly, which is exactly what prune would do
// for that one ref. Any other failure (remote unreachable, not a
// repository) is returned: the view could not be refreshed, and the caller
// must not treat the cached count as the truth. A repository with no remotes
// has nothing to refresh and returns nil.
func (g *GitWorktree) RefreshRemoteBranch() error {
	out, err := g.runGitCommand(g.repoPath, "remote")
	if err != nil {
		return fmt.Errorf("could not list remotes of %s: %w", g.repoPath, err)
	}
	for _, remote := range strings.Fields(out) {
		tracking := fmt.Sprintf("refs/remotes/%s/%s", remote, g.branchName)
		refspec := fmt.Sprintf("+refs/heads/%s:%s", g.branchName, tracking)
		_, err := g.runGitCommand(g.repoPath, "fetch", "--quiet", remote, refspec)
		if err == nil {
			continue
		}
		if strings.Contains(err.Error(), "couldn't find remote ref") {
			// The remote does not have the branch. A tracking ref left over
			// from an earlier fetch would make deleted-on-remote work look
			// safe; drop it. Absent already is fine.
			_, _ = g.runGitCommand(g.repoPath, "update-ref", "-d", tracking)
			continue
		}
		return fmt.Errorf("could not refresh %s from remote %s: %w", g.branchName, remote, err)
	}
	return nil
}
