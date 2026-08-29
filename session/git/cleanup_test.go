package git

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// makeRepo builds a throwaway repo with one commit on `main`.
//
// ⛔ EVERY TEST IN THIS FILE USES THROWAWAY REPOS UNDER t.TempDir(). The failure
// mode of getting this code wrong is deleting branches in a real repository, so
// nothing here is allowed to point at one.
func makeRepo(t *testing.T, root, name string) string {
	t.Helper()
	repo := filepath.Join(root, name)
	if err := os.MkdirAll(repo, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", repo, err)
	}
	run(t, repo, "init", "-q", "-b", "main")
	run(t, repo, "config", "user.email", "t@t")
	run(t, repo, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("x\n"), 0644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	run(t, repo, "add", "a.txt")
	run(t, repo, "commit", "-qm", "c1")
	return repo
}

// addSquadWorktree cuts a worktree for `branch` at the path squad would use --
// <worktreeRoot>/<branch>, which NESTS when the branch name has a slash.
func addSquadWorktree(t *testing.T, repo, worktreeRoot, branch string) string {
	t.Helper()
	path := filepath.Join(worktreeRoot, filepath.FromSlash(branch))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir parent of %s: %v", path, err)
	}
	run(t, repo, "worktree", "add", "-q", "-b", branch, path, "main")
	return path
}

// withWorktreeRoot points getWorktreeDirectory at a temp HOME.
func withWorktreeRoot(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	original, had := os.LookupEnv("HOME")
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv("HOME", original)
		} else {
			_ = os.Unsetenv("HOME")
		}
	})
	root, err := getWorktreeDirectory()
	if err != nil {
		t.Fatalf("getWorktreeDirectory: %v", err)
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatalf("mkdir worktree root: %v", err)
	}
	return root
}

func branchExists(t *testing.T, repo, branch string) bool {
	t.Helper()
	_, err := runGitIn(repo, "show-ref", "--verify", "refs/heads/"+branch)
	return err == nil
}

// ⭐ THE DISCRIMINATION CONTROL THE TICKET DEMANDS.
//
// `x-1` is a PREFIX of `x-10`. The replaced code paired a directory to a branch
// with strings.Contains(path, entry.Name()), so the entry `x-1` matched the path
// ending in `x-10` -- and whichever the map iteration reached first won. A fix
// that deleted BOTH would pass a single-case test, which is why the assertion
// that matters is on the branch that must SURVIVE.
func TestPlanPairsPrefixNamesExactly(t *testing.T) {
	root := t.TempDir()
	repo := makeRepo(t, root, "repo")
	wtRoot := withWorktreeRoot(t)

	// ⚠ THREE SHAPES OF COLLISION, AND ONLY ONE OF THEM BITES DETERMINISTICALLY.
	// MEASURED while writing this test, and it corrects the obvious assumption:
	//
	//   git worktree list --porcelain EMITS PATHS SORTED, not in registration
	//   order. So a PREFIX always sorts before what extends it, and a naive
	//   Contains scan over that output pairs x-1 CORRECTLY by luck.
	//
	// The replaced code did not scan sorted output -- it iterated a Go MAP, whose
	// order is random. That is why the defect was intermittent and why a test
	// built on prefixes alone would have been flaky in the direction of passing.
	//
	//   x-1 ⊂ x-10          prefix    -- collides only on an unlucky map order
	//   nag-24 ⊂ w-nag-24   contained -- same; the container sorts LAST here.
	//                                    This is the real pair: this estate has
	//                                    run w-nag-200 alongside nag-200.
	//   nag-24 ⊂ a-nag-24   contained -- the container sorts FIRST, so a Contains
	//                                    scan mispairs under EVERY order. This is
	//                                    the case that makes the test able to fail.
	paths := map[string]string{}
	for _, b := range []string{"x-10", "x-1", "w-nag-24", "nag-24", "a-nag-24"} {
		paths[b] = addSquadWorktree(t, repo, wtRoot, b)
	}

	plan, err := PlanCleanup(nil)
	if err != nil {
		t.Fatalf("PlanCleanup: %v", err)
	}

	got := map[string]string{}
	for _, tg := range plan.Targets {
		got[tg.Path] = tg.Branch
	}
	if len(plan.Targets) != 5 {
		t.Fatalf("expected 5 targets, got %d: %+v", len(plan.Targets), plan.Targets)
	}
	// ⛔ EVERY directory must carry ITS OWN branch. Under strings.Contains at
	// least one of these four pairs to its prefix-sibling.
	for branch, path := range paths {
		if got[path] != branch {
			t.Errorf("PREFIX COLLISION: worktree %s paired to branch %q, want %q",
				path, got[path], branch)
		}
	}
	if len(plan.Orphans) != 0 {
		t.Errorf("unexpected orphans: %+v", plan.Orphans)
	}
}

// The execution half of the same control: after a reset both branches must be
// gone from THIS repo, and -- the load-bearing half -- an unrelated branch in
// the SAME repo that had no worktree must survive.
func TestExecuteDeletesOnlyWorktreeBranches(t *testing.T) {
	root := t.TempDir()
	repo := makeRepo(t, root, "repo")
	wtRoot := withWorktreeRoot(t)

	addSquadWorktree(t, repo, wtRoot, "x-1")
	addSquadWorktree(t, repo, wtRoot, "x-10")
	run(t, repo, "branch", "keep-me") // no worktree: must survive

	plan, err := PlanCleanup(nil)
	if err != nil {
		t.Fatalf("PlanCleanup: %v", err)
	}
	res, err := ExecuteCleanup(plan)
	if err != nil {
		t.Fatalf("ExecuteCleanup: %v", err)
	}
	if len(res.Failures) != 0 {
		t.Fatalf("failures: %v", res.Failures)
	}
	if branchExists(t, repo, "x-1") {
		t.Error("x-1 survived the reset")
	}
	if branchExists(t, repo, "x-10") {
		t.Error("x-10 survived the reset")
	}
	// ⛔ NEGATIVE HALF: a branch with no worktree is not reset's business.
	if !branchExists(t, repo, "keep-me") {
		t.Error("keep-me was DELETED -- reset reached a branch that had no worktree")
	}
	if res.BranchesDeleted != 2 {
		t.Errorf("BranchesDeleted = %d, want 2", res.BranchesDeleted)
	}
}

// ⛔ THE SCOPING CONTROL. The replaced code ran `git branch -D` with no directory
// at all, so it deleted in whatever repo the process happened to be standing in.
// Here the process cwd is repo B while every worktree belongs to repo A, and B
// carries a branch with the SAME NAME as one of A's. B's must survive untouched.
func TestExecuteIgnoresTheProcessWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	repoA := makeRepo(t, root, "repoA")
	repoB := makeRepo(t, root, "repoB")
	wtRoot := withWorktreeRoot(t)

	addSquadWorktree(t, repoA, wtRoot, "shared-name")
	run(t, repoB, "branch", "shared-name") // same name, different repo, no worktree

	// Stand in repoB, exactly as a user in the wrong directory would.
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(repoB); err != nil {
		t.Fatalf("chdir repoB: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })

	plan, err := PlanCleanup(nil)
	if err != nil {
		t.Fatalf("PlanCleanup: %v", err)
	}
	if repos := plan.Repos(); len(repos) != 1 || NormalizeRepoPath(repos[0]) != NormalizeRepoPath(repoA) {
		t.Fatalf("plan touches %v; want only %s", repos, repoA)
	}
	if _, err := ExecuteCleanup(plan); err != nil {
		t.Fatalf("ExecuteCleanup: %v", err)
	}

	if branchExists(t, repoA, "shared-name") {
		t.Error("repoA/shared-name survived: the worktree's own branch was not deleted")
	}
	// ⛔ THE DEFECT, IF IT COMES BACK: this is the branch the old code destroyed.
	if !branchExists(t, repoB, "shared-name") {
		t.Error("repoB/shared-name was DELETED -- reset acted on the process working directory")
	}
}

// Nested layout: a branch name with a slash produces <root>/<prefix>/<name>, and
// a flat os.ReadDir of the root sees only the PREFIX.
//
// ⚠ MEASURED 2026-08-29 on the real machine: ~/.claude-squad/worktrees held ONE
// top-level entry, `rakizi`, containing nine worktrees across two repos. The
// replaced code would have removed that whole container while deleting one
// arbitrary branch.
func TestPlanFindsNestedWorktrees(t *testing.T) {
	root := t.TempDir()
	repo := makeRepo(t, root, "repo")
	wtRoot := withWorktreeRoot(t)

	p := addSquadWorktree(t, repo, wtRoot, "rakizi/lab-33")

	// The naive read the old code did.
	flat, err := os.ReadDir(wtRoot)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(flat) != 1 || flat[0].Name() != "rakizi" {
		t.Fatalf("setup is broken: the flat read must see only the prefix, got %v", flat)
	}

	plan, err := PlanCleanup(nil)
	if err != nil {
		t.Fatalf("PlanCleanup: %v", err)
	}
	if len(plan.Targets) != 1 || plan.Targets[0].Path != p {
		t.Fatalf("nested worktree not found: %+v", plan.Targets)
	}
	if plan.Targets[0].Branch != "rakizi/lab-33" {
		t.Errorf("branch = %q, want rakizi/lab-33", plan.Targets[0].Branch)
	}
	// ⛔ The container must NOT be reported as something to destroy on its own.
	for _, o := range plan.Orphans {
		if o.Path == filepath.Join(wtRoot, "rakizi") {
			t.Error("the prefix container was classified as an orphan directory")
		}
	}
}

// A pre-existing branch is the user's, not the session's. Instance.Cleanup
// already refuses to delete one; reset must agree.
func TestProtectedBranchSurvivesReset(t *testing.T) {
	root := t.TempDir()
	repo := makeRepo(t, root, "repo")
	wtRoot := withWorktreeRoot(t)

	addSquadWorktree(t, repo, wtRoot, "mine-already")
	addSquadWorktree(t, repo, wtRoot, "session-made")

	protected := map[string]bool{ProtectedKey(repo, "mine-already"): true}
	plan, err := PlanCleanup(protected)
	if err != nil {
		t.Fatalf("PlanCleanup: %v", err)
	}
	res, err := ExecuteCleanup(plan)
	if err != nil {
		t.Fatalf("ExecuteCleanup: %v", err)
	}
	if !branchExists(t, repo, "mine-already") {
		t.Error("a PRE-EXISTING branch was deleted by reset")
	}
	if branchExists(t, repo, "session-made") {
		t.Error("session-made survived: protection leaked to a branch it does not cover")
	}
	if res.BranchesKept != 1 || res.BranchesDeleted != 1 {
		t.Errorf("kept=%d deleted=%d, want 1 and 1", res.BranchesKept, res.BranchesDeleted)
	}
}

// A directory no repository claims loses its DIRECTORY and nothing else.
//
// ⭐ This is the third state kept apart: "resolved" / "orphan" / "not examined".
// The old code collapsed orphan into resolved by deleting a branch chosen with a
// substring match.
func TestOrphanDirectoryTouchesNoBranch(t *testing.T) {
	root := t.TempDir()
	repo := makeRepo(t, root, "repo")
	wtRoot := withWorktreeRoot(t)

	addSquadWorktree(t, repo, wtRoot, "real-one")
	// An orphan whose name is a substring of a real branch, to make the old
	// pairing maximally tempting.
	orphan := filepath.Join(wtRoot, "real")
	if err := os.MkdirAll(orphan, 0755); err != nil {
		t.Fatalf("mkdir orphan: %v", err)
	}
	if err := os.WriteFile(filepath.Join(orphan, "junk.txt"), []byte("x\n"), 0644); err != nil {
		t.Fatalf("write junk: %v", err)
	}

	plan, err := PlanCleanup(nil)
	if err != nil {
		t.Fatalf("PlanCleanup: %v", err)
	}
	var orphanPaths []string
	for _, o := range plan.Orphans {
		orphanPaths = append(orphanPaths, o.Path)
	}
	sort.Strings(orphanPaths)
	if len(orphanPaths) != 1 || orphanPaths[0] != orphan {
		t.Fatalf("orphans = %v, want [%s]", orphanPaths, orphan)
	}
	if plan.Orphans[0].Branch != "" {
		t.Errorf("an orphan was given a branch (%q) -- that is the guess this fix removes",
			plan.Orphans[0].Branch)
	}

	if _, err := ExecuteCleanup(plan); err != nil {
		t.Fatalf("ExecuteCleanup: %v", err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("orphan directory survived: %v", err)
	}
}

// Unpushed work is COUNTED, and "could not count" is not zero.
func TestUnpushedIsCountedAndSeparableFromUnknown(t *testing.T) {
	root := t.TempDir()
	repo := makeRepo(t, root, "repo")
	wtRoot := withWorktreeRoot(t)

	p := addSquadWorktree(t, repo, wtRoot, "has-work")
	if err := os.WriteFile(filepath.Join(p, "new.txt"), []byte("only copy\n"), 0644); err != nil {
		t.Fatalf("write new.txt: %v", err)
	}
	run(t, p, "add", "new.txt")
	run(t, p, "commit", "-qm", "the only copy of this work")
	addSquadWorktree(t, repo, wtRoot, "no-work")

	plan, err := PlanCleanup(nil)
	if err != nil {
		t.Fatalf("PlanCleanup: %v", err)
	}
	byBranch := map[string]CleanupTarget{}
	for _, tg := range plan.Targets {
		byBranch[tg.Branch] = tg
	}
	if got := byBranch["has-work"].Unpushed; got != 2 {
		// 2 = the base commit + the new one; neither is on a remote (there is none).
		t.Errorf("has-work Unpushed = %d, want 2", got)
	}
	// ⛔ THE NEGATIVE HALF: the counter must discriminate, not report work
	// everywhere. no-work has only the base commit, so it is 1, not 2.
	if got := byBranch["no-work"].Unpushed; got != 1 {
		t.Errorf("no-work Unpushed = %d, want 1", got)
	}
	withWork, uncounted := plan.UnpushedTotal()
	if withWork != 2 || uncounted != 0 {
		t.Errorf("UnpushedTotal = (%d, %d), want (2, 0)", withWork, uncounted)
	}

	// -1 means UNKNOWN and must never be produced by a branch that exists.
	if n := countUnpushed(repo, "no-such-branch"); n != -1 {
		t.Errorf("countUnpushed on a missing branch = %d, want -1 (could not look)", n)
	}
}

// An empty worktree root is a valid answer, not an error, and produces no plan.
func TestPlanOnEmptyRootIsEmptyNotAnError(t *testing.T) {
	withWorktreeRoot(t)
	plan, err := PlanCleanup(nil)
	if err != nil {
		t.Fatalf("PlanCleanup on an empty root: %v", err)
	}
	if len(plan.Targets)+len(plan.Orphans)+len(plan.NotExamined) != 0 {
		t.Errorf("empty root produced a plan: %+v", plan)
	}
}

// Every git call in cleanup.go must carry an explicit directory.
//
// ⭐ A MECHANISM, NOT A CONVENTION. The three defects in Rakizi/the-lab#33 were
// all one root cause -- exec.Command("git", ...) with no directory -- and a
// reviewer cannot be relied on to spot the fourth. This fails if one comes back.
func TestNoGitCallInCleanupInheritsTheWorkingDirectory(t *testing.T) {
	src, err := os.ReadFile("cleanup.go")
	if err != nil {
		t.Fatalf("read cleanup.go: %v", err)
	}
	text := string(src)

	// Positive control: the detector must be able to see the one legitimate
	// exec.Command in this file, the one inside runGitIn.
	if strings.Count(text, `exec.Command("git"`) != 1 {
		t.Fatalf("expected exactly one exec.Command(\"git\") in cleanup.go, found %d",
			strings.Count(text, `exec.Command("git"`))
	}
	if !strings.Contains(text, `exec.Command("git", append([]string{"-C", dir}, args...)...)`) {
		t.Error("the single git invocation no longer passes an explicit -C directory")
	}
}
