package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// ⭐ THE POSITIVE CONTROL AND THE NEGATIVE ARE THE SAME REPO, ONE STEP APART:
// the parent checkout is moved onto a feature branch, and the base must NOT
// follow it. Without the fix this test fails -- that is what makes it a test
// and not a restatement.
func TestBaseRefIgnoresParentCheckoutBranch(t *testing.T) {
	root := t.TempDir()
	bare := filepath.Join(root, "remote.git")
	run(t, root, "init", "-q", "--bare", "-b", "develop", bare)

	repo := filepath.Join(root, "work")
	run(t, root, "clone", "-q", bare, repo)
	run(t, repo, "config", "user.email", "t@t")
	run(t, repo, "config", "user.name", "t")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("base\n"), 0644)
	run(t, repo, "add", "a.txt")
	run(t, repo, "commit", "-qm", "base commit")
	run(t, repo, "push", "-q", "origin", "develop")
	developSHA := run(t, repo, "rev-parse", "HEAD")

	// The shared checkout wanders onto a feature branch and commits -- the exact
	// thing the reflog showed happening in ~/the-lab/NextActionGuide.
	run(t, repo, "checkout", "-q", "-b", "some/feature")
	os.WriteFile(filepath.Join(repo, "b.txt"), []byte("unrelated\n"), 0644)
	run(t, repo, "add", "b.txt")
	run(t, repo, "commit", "-qm", "unrelated work in the shared checkout")
	featureSHA := run(t, repo, "rev-parse", "HEAD")

	if developSHA == featureSHA {
		t.Fatal("setup is broken: the two SHAs must differ or this proves nothing")
	}

	g := &GitWorktree{repoPath: repo}
	ref, err := g.resolveBaseRef()
	if err != nil {
		t.Fatalf("resolveBaseRef: %v", err)
	}
	got := run(t, repo, "rev-parse", ref)

	if got != developSHA {
		t.Errorf("base resolved to %s (%s); want develop %s", ref, got[:9], developSHA[:9])
	}
	// ⛔ the negative half: it must not be the feature branch it happened to be on
	if got == featureSHA {
		t.Errorf("BASE FOLLOWED THE PARENT CHECKOUT -- this is the defect, unfixed")
	}
	t.Logf("parent was on some/feature (%s); base correctly resolved to %s (%s)",
		featureSHA[:9], ref, got[:9])
}

// A repo with NO remote must still work: local HEAD is genuinely the only base.
func TestBaseRefNoRemoteUsesHEAD(t *testing.T) {
	repo := t.TempDir()
	run(t, repo, "init", "-q", "-b", "main")
	run(t, repo, "config", "user.email", "t@t")
	run(t, repo, "config", "user.name", "t")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("x\n"), 0644)
	run(t, repo, "add", "a.txt")
	run(t, repo, "commit", "-qm", "c1")

	g := &GitWorktree{repoPath: repo}
	ref, err := g.resolveBaseRef()
	if err != nil {
		t.Fatalf("a repo with no remote must resolve, not error: %v", err)
	}
	if ref != "HEAD" {
		t.Errorf("no-remote repo: got %q, want HEAD", ref)
	}
}

// ⭐ DISCRIMINATION: the override must actually override, or the env var is
// decoration. Same repo as the first test, different answer.
func TestBaseRefOverride(t *testing.T) {
	root := t.TempDir()
	bare := filepath.Join(root, "remote.git")
	run(t, root, "init", "-q", "--bare", "-b", "develop", bare)
	repo := filepath.Join(root, "work")
	run(t, root, "clone", "-q", bare, repo)
	run(t, repo, "config", "user.email", "t@t")
	run(t, repo, "config", "user.name", "t")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("x\n"), 0644)
	run(t, repo, "add", "a.txt")
	run(t, repo, "commit", "-qm", "c1")
	run(t, repo, "push", "-q", "origin", "develop")

	t.Setenv("CLAUDE_SQUAD_BASE_REF", "refs/heads/develop")
	g := &GitWorktree{repoPath: repo}
	ref, err := g.resolveBaseRef()
	if err != nil {
		t.Fatalf("resolveBaseRef: %v", err)
	}
	if ref != "refs/heads/develop" {
		t.Errorf("override ignored: got %q", ref)
	}
}
