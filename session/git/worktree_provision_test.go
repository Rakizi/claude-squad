package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ⛔ THE CONVENTION-VS-MECHANISM TEST. Workers were *told* to stay in their
// worktree; nothing *made* them, and a fresh worktree could not run a single test
// (no .venv), so the only way to do the job was the shared checkout -- where a
// pr-audit child ran `git stash` + `git checkout` on a tree five bots share.
//
// This test proves Setup() ITSELF provisions, so isolation stops depending on an
// agent remembering. It asserts the provisioner is invoked with the new worktree's
// path -- the mechanism, not a message about the mechanism.
func TestSetupProvisionsTheNewWorktree(t *testing.T) {
	tempHome := t.TempDir()
	originalHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tempHome); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Stub provisioner at the path Setup() looks for, recording its argument.
	binDir := filepath.Join(tempHome, "the-lab", "ops", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir ops/bin: %v", err)
	}
	marker := filepath.Join(tempHome, "provisioned-path")
	script := filepath.Join(binDir, "worktree-provision")
	body := "#!/bin/sh\nprintf '%s' \"$1\" > " + marker + "\n"
	if err := os.WriteFile(script, []byte(body), 0755); err != nil {
		t.Fatalf("write stub provisioner: %v", err)
	}

	repoPath := filepath.Join(t.TempDir(), "repo")
	mustRunGit(t, "", "init", repoPath)
	mustRunGit(t, repoPath, "config", "user.name", "Test User")
	mustRunGit(t, repoPath, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("hello\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	mustRunGit(t, repoPath, "add", "README.md")
	mustRunGit(t, repoPath, "commit", "-m", "initial")
	mustRunGit(t, repoPath, "branch", "feature/prov")

	worktreePath := filepath.Join(tempHome, ".claude-squad", "worktrees", "feature-prov")
	g := &GitWorktree{
		repoPath:         repoPath,
		worktreePath:     worktreePath,
		branchName:       "feature/prov",
		isExistingBranch: true,
	}

	if err := g.Setup(); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}

	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("provisioner was never invoked: %v", err)
	}
	if strings.TrimSpace(string(got)) != worktreePath {
		t.Fatalf("provisioner ran for %q, want %q", strings.TrimSpace(string(got)), worktreePath)
	}
}

// ⭐ NEVER FATAL. A failing or absent provisioner must not cost a session its
// worktree: the work is already safely created by the time provision() runs. A repo
// with no Python manifest is the normal case, not an error.
func TestSetupSurvivesAFailingProvisioner(t *testing.T) {
	tempHome := t.TempDir()
	originalHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tempHome); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	binDir := filepath.Join(tempHome, "the-lab", "ops", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir ops/bin: %v", err)
	}
	// Exit 3 == could-not-look, the provisioner's own blind state.
	script := filepath.Join(binDir, "worktree-provision")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 3\n"), 0755); err != nil {
		t.Fatalf("write failing provisioner: %v", err)
	}

	repoPath := filepath.Join(t.TempDir(), "repo")
	mustRunGit(t, "", "init", repoPath)
	mustRunGit(t, repoPath, "config", "user.name", "Test User")
	mustRunGit(t, repoPath, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("hello\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	mustRunGit(t, repoPath, "add", "README.md")
	mustRunGit(t, repoPath, "commit", "-m", "initial")
	mustRunGit(t, repoPath, "branch", "feature/blind")

	worktreePath := filepath.Join(tempHome, ".claude-squad", "worktrees", "feature-blind")
	g := &GitWorktree{
		repoPath:         repoPath,
		worktreePath:     worktreePath,
		branchName:       "feature/blind",
		isExistingBranch: true,
	}

	if err := g.Setup(); err != nil {
		t.Fatalf("Setup() must survive a failed provision, got error = %v", err)
	}
	// The worktree must still exist and be usable.
	if _, err := os.Stat(filepath.Join(worktreePath, "README.md")); err != nil {
		t.Fatalf("worktree was not created despite provisioner failure: %v", err)
	}
}
