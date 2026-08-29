package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ⛔ WHY THIS FILE EXISTS: `cs reset` used to delete branches in WHATEVER REPO
// THE USER WAS STANDING IN.
//
// The version this replaces ran `git worktree list --porcelain`, `git branch -D`
// and `git worktree prune` with neither `-C` nor `cmd.Dir` -- so all three
// inherited the process working directory. On a machine holding seven repos that
// is a coin toss, and the loser is a branch nobody pushed. It then paired a
// directory to a branch with `strings.Contains(path, entry.Name())`, so
// `x-1` matched a path containing `x-10`, while `os.RemoveAll` deleted every
// directory on disk regardless of which repo the branch half had been scoped to.
// The two halves disagreed about what they were operating on.
//
// MEASURED 2026-08-29, ~/.claude-squad/worktrees: the top level holds exactly ONE
// entry, `rakizi`, a container for nine worktrees across TWO repos. The old
// ReadDir saw that one entry, matched `strings.Contains(path, "rakizi")` -- true
// of every path under /home/rakizi -- and would have `os.RemoveAll`ed all nine
// worktrees while deleting a single arbitrary branch in the accidental cwd.
//
// ⭐ THE SHAPE OF THE FIX: a worktree directory is asked WHICH REPO OWNS IT and
// WHICH BRANCH IT HAS CHECKED OUT, by running git INSIDE that directory. No
// global listing, no cwd, no substring pairing -- the pairing is not a match at
// all, it is a read. Both halves then act on the same resolved repo.

// maxWorktreeSearchDepth bounds the walk under the worktree root.
//
// Squad's directory name is the SANITIZED BRANCH NAME, and sanitizeBranchName
// keeps `/`, so `rakizi/lab-33` becomes a nested directory. Nesting is real and
// a flat ReadDir cannot see it. The bound exists so a symlink loop or a stray
// deep tree cannot make this walk unbounded -- anything below it is reported as
// NOT EXAMINED rather than silently skipped.
const maxWorktreeSearchDepth = 8

// CleanupTarget is one directory under the squad worktree root, resolved to the
// repository that owns it.
//
// ⭐ THREE OUTCOMES, NOT TWO. A directory is either resolved (RepoPath and Branch
// read from git inside it), an ORPHAN (git could not resolve it -- the directory
// exists but no repo claims it), or NOT EXAMINED (below the depth bound). The
// third is what an unbounded-but-silent walk turns into a clean-looking zero.
type CleanupTarget struct {
	// Path is the directory on disk.
	Path string
	// RepoPath is the repository that owns this worktree. Empty for an orphan.
	RepoPath string
	// Branch is the branch checked out in it. Empty when HEAD is detached, or
	// for an orphan.
	Branch string
	// Unpushed counts commits on Branch that are on NO remote -- the work that
	// `git branch -D` would destroy for good. -1 means it could not be counted,
	// which is NOT the same as zero.
	Unpushed int
	// Protected is true when the branch existed before the session was created.
	// Instance.Cleanup already refuses to delete these; reset now agrees.
	Protected bool
	// Why records the reason an orphan could not be resolved.
	Why string
}

// CleanupPlan is what `cs reset` will do, computed before anything is destroyed.
type CleanupPlan struct {
	// Root is the squad worktree directory the plan covers.
	Root string
	// Targets are directories resolved to a repo.
	Targets []CleanupTarget
	// Orphans are directories no repo claims. Their DIRECTORY is removed; no
	// branch is touched, because none could be identified.
	Orphans []CleanupTarget
	// NotExamined are directories below the depth bound. Nothing is done to
	// them, and they are named rather than dropped.
	NotExamined []string
}

// CleanupResult records what actually happened, verified at the destination.
type CleanupResult struct {
	WorktreesRemoved int
	BranchesDeleted  int
	OrphansRemoved   int
	BranchesKept     int
	// Failures name what was supposed to be gone and still is not. Read back
	// from disk and from git, never inferred from an exit code.
	Failures []string
}

// runGitIn runs git with an explicit working directory.
//
// ⛔ EVERY GIT CALL IN THIS FILE GOES THROUGH HERE, and `dir` is never optional.
// That is the whole point: the defect this file fixes was three exec.Command
// calls with no directory at all.
func runGitIn(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(out)), fmt.Errorf("git %s in %s: %w: %s",
			strings.Join(args, " "), dir, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// resolveOwningRepo answers "which repository does this worktree belong to?" by
// asking git from INSIDE the worktree.
//
// --git-common-dir is the repository's real .git, shared by every worktree of
// it; a worktree's own --git-dir is the per-worktree admin directory and would
// point back at itself. --path-format=absolute is needed because git returns a
// relative path when the answer is the local .git.
func resolveOwningRepo(worktreePath string) (string, error) {
	out, err := runGitIn(worktreePath, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	commonDir := strings.TrimSpace(out)
	if commonDir == "" {
		return "", fmt.Errorf("git returned an empty common dir for %s", worktreePath)
	}
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(worktreePath, commonDir)
	}
	return NormalizeRepoPath(filepath.Dir(commonDir)), nil
}

// NormalizeRepoPath makes two spellings of the same repository compare equal.
//
// ⚠ The protected-branch set is keyed by repo path, and state.json stores a path
// produced by a DIFFERENT call (findGitRepoRoot). A symlinked or relative
// spelling on one side and not the other would silently fail every lookup, so
// both sides go through here -- a key that never matches is a protection that
// never fires, and nothing would say so.
func NormalizeRepoPath(p string) string {
	if p == "" {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return filepath.Clean(abs)
}

// resolveCheckedOutBranch returns the branch a worktree has checked out.
//
// An empty string with a nil error means DETACHED HEAD -- a real, valid state
// with no branch to delete. symbolic-ref exits non-zero in exactly that case, so
// it is distinguished here rather than collapsed into an error.
func resolveCheckedOutBranch(worktreePath string) (string, error) {
	out, err := runGitIn(worktreePath, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		// Detached HEAD, or a HEAD that cannot be read. Tell them apart.
		if _, headErr := runGitIn(worktreePath, "rev-parse", "--verify", "HEAD"); headErr == nil {
			return "", nil // detached, but a valid commit: no branch exists to delete
		}
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// countUnpushed counts commits on branch that exist on NO remote.
//
// ⭐ THIS IS THE NUMBER THE CONFIRM EXISTS FOR. `git branch -D` on a branch whose
// commits are on a remote is an inconvenience; on a branch whose commits are not,
// it is the only copy. MEASURED 2026-08-29 while this was being written: one
// squad session held 2 commits, 1,142 lines, that existed nowhere else.
//
// Returns -1 when the count could not be taken. -1 is NOT 0.
func countUnpushed(repoPath, branch string) int {
	if branch == "" {
		return 0
	}
	out, err := runGitIn(repoPath, "rev-list", "--count", branch, "--not", "--remotes")
	if err != nil {
		return -1
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return -1
	}
	return n
}

// discoverWorktrees walks the squad worktree root and separates three things:
// directories that ARE worktrees, directories that are not and hold none
// (orphans), and directories the depth bound stopped it from examining.
//
// ⛔ A FLAT os.ReadDir CANNOT SEE A SQUAD WORKTREE ON THIS MACHINE. Branch names
// contain slashes, so the layout is <root>/<prefix>/<name>. The old code read one
// level, found the PREFIX directory, and treated that container as a worktree.
func discoverWorktrees(root string) (worktrees []string, orphans []string, notExamined []string, err error) {
	var walk func(dir string, depth int) (foundBelow bool, err error)
	walk = func(dir string, depth int) (bool, error) {
		if depth > maxWorktreeSearchDepth {
			notExamined = append(notExamined, dir)
			return true, nil // treat as "something may be here": never remove it
		}
		entries, readErr := os.ReadDir(dir)
		if readErr != nil {
			return false, readErr
		}
		found := false
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			p := filepath.Join(dir, e.Name())
			if _, statErr := os.Lstat(filepath.Join(p, ".git")); statErr == nil {
				worktrees = append(worktrees, p)
				found = true
				continue // a worktree is a leaf: never descend into a checkout
			}
			sub, walkErr := walk(p, depth+1)
			if walkErr != nil {
				return found, walkErr
			}
			if sub {
				found = true
			} else {
				orphans = append(orphans, p)
			}
		}
		return found, nil
	}

	if _, statErr := os.Stat(root); os.IsNotExist(statErr) {
		return nil, nil, nil, nil // nothing to clean is a valid, empty answer
	}
	if _, walkErr := walk(root, 0); walkErr != nil {
		return nil, nil, nil, walkErr
	}
	sort.Strings(worktrees)
	sort.Strings(orphans)
	sort.Strings(notExamined)
	return worktrees, orphans, notExamined, nil
}

// PlanCleanup computes what `cs reset` would destroy, WITHOUT destroying any of
// it. protectedBranches is keyed by protectedKey(repo, branch) and marks branches
// that existed before their session -- reset must not delete a branch it did not
// create, which is the rule Instance.Cleanup already follows.
func PlanCleanup(protectedBranches map[string]bool) (*CleanupPlan, error) {
	root, err := getWorktreeDirectory()
	if err != nil {
		return nil, fmt.Errorf("failed to get worktree directory: %w", err)
	}

	worktrees, orphanDirs, notExamined, err := discoverWorktrees(root)
	if err != nil {
		return nil, fmt.Errorf("failed to read worktree directory %s: %w", root, err)
	}

	plan := &CleanupPlan{Root: root, NotExamined: notExamined}

	for _, wt := range worktrees {
		repo, repoErr := resolveOwningRepo(wt)
		if repoErr != nil {
			plan.Orphans = append(plan.Orphans, CleanupTarget{
				Path: wt,
				Why:  fmt.Sprintf("no repository claims it: %v", repoErr),
			})
			continue
		}
		branch, branchErr := resolveCheckedOutBranch(wt)
		if branchErr != nil {
			plan.Orphans = append(plan.Orphans, CleanupTarget{
				Path: wt, RepoPath: repo,
				Why: fmt.Sprintf("HEAD could not be read: %v", branchErr),
			})
			continue
		}
		plan.Targets = append(plan.Targets, CleanupTarget{
			Path:      wt,
			RepoPath:  repo,
			Branch:    branch,
			Unpushed:  countUnpushed(repo, branch),
			Protected: protectedBranches[protectedKey(repo, branch)],
		})
	}

	for _, d := range orphanDirs {
		plan.Orphans = append(plan.Orphans, CleanupTarget{
			Path: d, Why: "not a git worktree and contains none",
		})
	}

	return plan, nil
}

// protectedKey names one branch in one repository. Repo AND branch, because the
// same branch name in two repos is two different branches -- which is the class
// of confusion this whole file exists to end.
func protectedKey(repoPath, branch string) string {
	return NormalizeRepoPath(repoPath) + "\x00" + branch
}

// ProtectedKey is the exported form for callers assembling the protected set.
func ProtectedKey(repoPath, branch string) string { return protectedKey(repoPath, branch) }

// UnpushedTotal reports how many branches in the plan carry commits that are on
// no remote, and how many could not be counted. The second number is the one a
// summary usually loses.
func (p *CleanupPlan) UnpushedTotal() (withWork int, uncounted int) {
	for _, t := range p.Targets {
		if t.Protected {
			continue
		}
		switch {
		case t.Unpushed < 0:
			uncounted++
		case t.Unpushed > 0:
			withWork++
		}
	}
	return withWork, uncounted
}

// Repos lists the distinct repositories the plan will touch, sorted.
func (p *CleanupPlan) Repos() []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range p.Targets {
		if t.RepoPath != "" && !seen[t.RepoPath] {
			seen[t.RepoPath] = true
			out = append(out, t.RepoPath)
		}
	}
	sort.Strings(out)
	return out
}

// ExecuteCleanup carries out a plan.
//
// ⭐ EVERY DESTRUCTIVE CALL IS SCOPED TO THE REPO THE TARGET RESOLVED TO, and the
// directory half and the branch half act on the SAME target. There is no path by
// which this removes a directory belonging to one repo while deleting a branch in
// another.
//
// ⛔ AND IT VERIFIES AT THE DESTINATION. `git worktree remove` and `git branch -D`
// are asked whether the thing is gone, not whether the command exited zero.
func ExecuteCleanup(plan *CleanupPlan) (*CleanupResult, error) {
	res := &CleanupResult{}

	for _, t := range plan.Targets {
		// Remove the worktree through the repo that owns it, so git's admin
		// files go with the directory.
		if _, err := runGitIn(t.RepoPath, "worktree", "remove", "--force", t.Path); err != nil {
			// The registration may already be gone. Fall through to the
			// directory removal and let the destination check decide.
			_ = os.RemoveAll(t.Path)
		}
		if _, err := os.Stat(t.Path); err == nil {
			_ = os.RemoveAll(t.Path)
		}
		if _, err := os.Stat(t.Path); err == nil {
			res.Failures = append(res.Failures, "worktree directory still present: "+t.Path)
		} else {
			res.WorktreesRemoved++
		}

		if t.Branch == "" {
			continue // detached HEAD: there is no branch to delete
		}
		if t.Protected {
			res.BranchesKept++
			continue
		}
		if _, err := runGitIn(t.RepoPath, "branch", "-D", t.Branch); err != nil {
			// Not necessarily a failure -- the branch may already be gone.
			// Ask the destination.
			if _, refErr := runGitIn(t.RepoPath, "show-ref", "--verify",
				"refs/heads/"+t.Branch); refErr == nil {
				res.Failures = append(res.Failures,
					fmt.Sprintf("branch %s still present in %s: %v", t.Branch, t.RepoPath, err))
				continue
			}
		}
		if _, refErr := runGitIn(t.RepoPath, "show-ref", "--verify",
			"refs/heads/"+t.Branch); refErr == nil {
			res.Failures = append(res.Failures,
				fmt.Sprintf("branch %s still present in %s after branch -D", t.Branch, t.RepoPath))
			continue
		}
		res.BranchesDeleted++
	}

	// ⛔ AN ORPHAN LOSES ITS DIRECTORY AND NOTHING ELSE. No branch is deleted for
	// a target whose owning repo could not be established -- that guess is what
	// made the old code dangerous.
	for _, o := range plan.Orphans {
		_ = os.RemoveAll(o.Path)
		if _, err := os.Stat(o.Path); err == nil {
			res.Failures = append(res.Failures, "orphan directory still present: "+o.Path)
		} else {
			res.OrphansRemoved++
		}
	}

	// Prune once per repo actually touched -- again with an explicit directory.
	for _, repo := range plan.Repos() {
		if _, err := runGitIn(repo, "worktree", "prune"); err != nil {
			res.Failures = append(res.Failures, fmt.Sprintf("worktree prune failed in %s: %v", repo, err))
		}
	}

	pruneEmptyDirs(plan.Root)
	return res, nil
}

// pruneEmptyDirs drops the container directories a nested layout leaves behind
// (the `rakizi/` in <root>/rakizi/<name>), bottom-up, never the root itself.
func pruneEmptyDirs(root string) {
	var walk func(dir string) bool
	walk = func(dir string) bool {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return false
		}
		empty := true
		for _, e := range entries {
			if !e.IsDir() {
				empty = false
				continue
			}
			p := filepath.Join(dir, e.Name())
			if !walk(p) {
				empty = false
				continue
			}
			if err := os.Remove(p); err != nil {
				empty = false
			}
		}
		return empty
	}
	walk(root)
}
