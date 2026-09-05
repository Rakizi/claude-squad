package git

import (
	"claude-squad/log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupFromExistingBranch_RemovesOrphanedDirectory(t *testing.T) {
	tempHome := t.TempDir()
	originalHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tempHome); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	defer func() {
		_ = os.Setenv("HOME", originalHome)
	}()

	repoPath := filepath.Join(t.TempDir(), "repo")
	mustRunGit(t, "", "init", repoPath)
	mustRunGit(t, repoPath, "config", "user.name", "Test User")
	mustRunGit(t, repoPath, "config", "user.email", "test@example.com")

	readmePath := filepath.Join(repoPath, "README.md")
	if err := os.WriteFile(readmePath, []byte("hello\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	mustRunGit(t, repoPath, "add", "README.md")
	mustRunGit(t, repoPath, "commit", "-m", "initial")
	mustRunGit(t, repoPath, "branch", "feature/test")

	worktreePath := filepath.Join(tempHome, ".claude-squad", "worktrees", "feature-test")
	if err := os.MkdirAll(worktreePath, 0755); err != nil {
		t.Fatalf("mkdir orphaned worktree: %v", err)
	}

	junkPath := filepath.Join(worktreePath, "orphan.txt")
	if err := os.WriteFile(junkPath, []byte("orphaned\n"), 0644); err != nil {
		t.Fatalf("write orphan marker: %v", err)
	}

	g := &GitWorktree{
		repoPath:         repoPath,
		worktreePath:     worktreePath,
		branchName:       "feature/test",
		isExistingBranch: true,
	}

	if err := g.Setup(); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}

	if _, err := os.Stat(junkPath); !os.IsNotExist(err) {
		t.Fatalf("orphan marker still exists after Setup, err = %v", err)
	}

	if valid, err := g.IsValidWorktree(); err != nil {
		t.Fatalf("IsValidWorktree() error = %v", err)
	} else if !valid {
		t.Fatal("expected Setup() to recreate a valid worktree")
	}

	currentBranch := mustRunGit(t, worktreePath, "branch", "--show-current")
	if currentBranch != "feature/test\n" {
		t.Fatalf("current branch = %q, want %q", currentBranch, "feature/test\n")
	}
}

func TestSetupFromExistingBranch_RecordsBaseCommit(t *testing.T) {
	tempHome := t.TempDir()
	originalHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tempHome); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	defer func() {
		_ = os.Setenv("HOME", originalHome)
	}()

	repoPath := filepath.Join(t.TempDir(), "repo")
	mustRunGit(t, "", "init", repoPath)
	mustRunGit(t, repoPath, "config", "user.name", "Test User")
	mustRunGit(t, repoPath, "config", "user.email", "test@example.com")

	readmePath := filepath.Join(repoPath, "README.md")
	if err := os.WriteFile(readmePath, []byte("hello\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	mustRunGit(t, repoPath, "add", "README.md")
	mustRunGit(t, repoPath, "commit", "-m", "initial")
	mustRunGit(t, repoPath, "branch", "feature/test")

	g := &GitWorktree{
		repoPath:         repoPath,
		worktreePath:     filepath.Join(tempHome, ".claude-squad", "worktrees", "feature-test"),
		branchName:       "feature/test",
		isExistingBranch: true,
	}

	if err := g.Setup(); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}

	want := strings.TrimSpace(mustRunGit(t, repoPath, "rev-parse", "feature/test"))
	if got := g.GetBaseCommitSHA(); got != want {
		t.Fatalf("GetBaseCommitSHA() = %q, want %q", got, want)
	}
}

func mustRunGit(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmdArgs := args
	if dir != "" {
		cmdArgs = append([]string{"-C", dir}, args...)
	}

	cmd := exec.Command("git", cmdArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
	return string(output)
}

// TestCleanupWorktrees_MultiRepoIsolation proves the two defects that made
// CleanupWorktrees dangerous (Rakizi/the-lab#33): it must operate on the repo
// each worktree ACTUALLY belongs to, not the launcher's cwd, and it must
// match a worktree to its branch by EXACT identity, never a substring.
//
// Setup: two SEPARATE repos, each with a worktree under the same shared
// ~/.claude-squad/worktrees/ root -- exactly how the real estate lays worktrees
// out across NAG/Tools/the-lab. A launcher cwd is set to a THIRD, unrelated
// directory, so the old bug (no cmd.Dir -> inherits cwd) cannot accidentally
// pass by cwd happening to match one of the two repos.
func TestCleanupWorktrees_MultiRepoIsolation(t *testing.T) {
	// ⛔ log.ErrorLog is only set by log.Initialize(), which the real app calls
	// at startup -- a test that never does hits a nil-pointer panic the first
	// time CleanupWorktrees logs an error. The ORIGINAL code had this same
	// unguarded log.ErrorLog.Printf call (worktree_ops.go, pre-fix line 236),
	// so this was already a live landmine for any future test; init it here
	// rather than remove the logging this fix relies on for visibility.
	log.Initialize(false)
	defer log.Close()

	tempHome := t.TempDir()
	originalHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tempHome); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	worktreesRoot := filepath.Join(tempHome, ".claude-squad", "worktrees")
	if err := os.MkdirAll(worktreesRoot, 0755); err != nil {
		t.Fatalf("mkdir worktrees root: %v", err)
	}

	setupRepo := func(name string) string {
		repoPath := filepath.Join(t.TempDir(), name)
		mustRunGit(t, "", "init", repoPath)
		mustRunGit(t, repoPath, "config", "user.name", "Test User")
		mustRunGit(t, repoPath, "config", "user.email", "test@example.com")
		readme := filepath.Join(repoPath, "README.md")
		if err := os.WriteFile(readme, []byte(name+"\n"), 0644); err != nil {
			t.Fatalf("write README for %s: %v", name, err)
		}
		mustRunGit(t, repoPath, "add", "README.md")
		mustRunGit(t, repoPath, "commit", "-m", "initial")
		return repoPath
	}

	// ⭐ Branch names deliberately chosen so a substring match (the old bug)
	// would confuse them: "w-nag-1" is a substring of a path containing
	// "w-nag-10". Exact-basename matching must tell them apart.
	repoA := setupRepo("repo-a")
	wtA := filepath.Join(worktreesRoot, "w-nag-1")
	mustRunGit(t, repoA, "worktree", "add", "-b", "w-nag-1", wtA)

	repoB := setupRepo("repo-b")
	wtB := filepath.Join(worktreesRoot, "w-nag-10")
	mustRunGit(t, repoB, "worktree", "add", "-b", "w-nag-10", wtB)

	// The launcher's cwd is a THIRD repo, unrelated to either worktree --
	// the old bug would run every git command here instead.
	launcherCwd := setupRepo("launcher-repo")
	originalWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(launcherCwd); err != nil {
		t.Fatalf("chdir to launcher repo: %v", err)
	}
	defer func() { _ = os.Chdir(originalWd) }()

	if err := CleanupWorktrees(); err != nil {
		t.Fatalf("CleanupWorktrees() error = %v", err)
	}

	// Both worktree DIRECTORIES must be gone.
	for _, wt := range []string{wtA, wtB} {
		if _, err := os.Stat(wt); !os.IsNotExist(err) {
			t.Errorf("worktree dir %s still exists after cleanup, err = %v", wt, err)
		}
	}

	// ⛔ THE ACTUAL REGRESSION TEST: each branch must be deleted from ITS OWN
	// repo. The old code ran `git branch -D` with no cmd.Dir (deleting from
	// the launcher's cwd repo, where the branch never existed -- a silent
	// no-op) and/or could pair the wrong worktree to the wrong branch via
	// substring match.
	branchesA := mustRunGit(t, repoA, "branch", "--list", "w-nag-1")
	if strings.Contains(branchesA, "w-nag-1") {
		t.Errorf("branch w-nag-1 still exists in repo-a after cleanup: %q", branchesA)
	}
	branchesB := mustRunGit(t, repoB, "branch", "--list", "w-nag-10")
	if strings.Contains(branchesB, "w-nag-10") {
		t.Errorf("branch w-nag-10 still exists in repo-b after cleanup: %q", branchesB)
	}

	// NEGATIVE CONTROL: the launcher repo was never touched -- it has no
	// worktrees of its own and must still be a normal, functioning repo.
	if out := mustRunGit(t, launcherCwd, "status", "--porcelain"); strings.TrimSpace(out) != "" {
		t.Errorf("launcher repo unexpectedly dirty after cleanup: %q", out)
	}
}
