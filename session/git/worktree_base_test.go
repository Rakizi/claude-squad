package git

import (
	"fmt"
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

// ⛔ THE REGRESSION TEST. NextActionGuide works on `develop` while its
// origin/HEAD points at `main`, and develop is 2,500 commits ahead. Preferring
// origin/HEAD cut new sessions from a 2,500-commit-stale base.
//
// ⭐ The earlier tests could not catch this: they built a repo whose remote
// default WAS the working branch, so both candidate rules gave the same answer.
// A control that cannot fail the way the subject fails is not a control.
func TestBaseRefPrefersCurrentBranchOverStaleRemoteHEAD(t *testing.T) {
	root := t.TempDir()
	bare := filepath.Join(root, "remote.git")
	run(t, root, "init", "-q", "--bare", "-b", "main", bare)

	repo := filepath.Join(root, "work")
	run(t, root, "clone", "-q", bare, repo)
	run(t, repo, "config", "user.email", "t@t")
	run(t, repo, "config", "user.name", "t")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("old release\n"), 0644)
	run(t, repo, "add", "a.txt")
	run(t, repo, "commit", "-qm", "main: an old release")
	run(t, repo, "push", "-q", "origin", "main")
	mainSHA := run(t, repo, "rev-parse", "HEAD")

	// develop races far ahead — the branch all the work actually happens on.
	run(t, repo, "checkout", "-q", "-b", "develop")
	for i := 0; i < 5; i++ {
		// distinct content each time, or git has nothing to commit after the first
		os.WriteFile(filepath.Join(repo, "a.txt"), []byte(fmt.Sprintf("dev work %d\n", i)), 0644)
		run(t, repo, "commit", "-qam", fmt.Sprintf("develop work %d", i))
	}
	run(t, repo, "push", "-q", "-u", "origin", "develop")
	developSHA := run(t, repo, "rev-parse", "HEAD")

	// origin/HEAD points at main — exactly NextActionGuide's real configuration.
	run(t, repo, "remote", "set-head", "origin", "main")
	if got := run(t, repo, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); got != "origin/main" {
		t.Fatalf("setup: origin/HEAD is %q, want origin/main", got)
	}

	g := &GitWorktree{repoPath: repo}
	ref, err := g.resolveBaseRef()
	if err != nil {
		t.Fatalf("resolveBaseRef: %v", err)
	}
	got := run(t, repo, "rev-parse", ref)

	if got == mainSHA {
		t.Errorf("⛔ BASE FOLLOWED A STALE origin/HEAD (%s) -- this is the regression", ref)
	}
	if got != developSHA {
		t.Errorf("base = %s (%s); want develop %s", ref, got[:9], developSHA[:9])
	}
	t.Logf("origin/HEAD=main, checkout on develop -> base correctly %s (%s)", ref, got[:9])
}
