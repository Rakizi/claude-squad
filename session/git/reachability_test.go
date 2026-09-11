package git

import (
	"os"
	"path/filepath"
	"testing"
)

// reachabilityRepo builds a clone with one pushed base commit on main and a
// `feat` branch holding two commits that exist ONLY in the clone. Every test
// below starts from this shape and moves one thing.
func reachabilityRepo(t *testing.T) (repo string) {
	t.Helper()
	root := t.TempDir()
	bare := filepath.Join(root, "remote.git")
	run(t, root, "init", "-q", "--bare", "-b", "main", bare)
	repo = filepath.Join(root, "work")
	run(t, root, "clone", "-q", bare, repo)
	run(t, repo, "config", "user.email", "t@t")
	run(t, repo, "config", "user.name", "t")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("base\n"), 0644)
	run(t, repo, "add", "a.txt")
	run(t, repo, "commit", "-qm", "base")
	run(t, repo, "push", "-q", "origin", "main")
	run(t, repo, "checkout", "-q", "-b", "feat")
	for _, f := range []string{"b.txt", "c.txt"} {
		os.WriteFile(filepath.Join(repo, f), []byte(f+"\n"), 0644)
		run(t, repo, "add", f)
		run(t, repo, "commit", "-qm", "local "+f)
	}
	run(t, repo, "checkout", "-q", "main")
	return repo
}

func TestLocalOnlyCommits(t *testing.T) {
	repo := reachabilityRepo(t)
	g := &GitWorktree{repoPath: repo, branchName: "feat"}

	t.Run("two commits pushed nowhere count as 2", func(t *testing.T) {
		n, err := g.LocalOnlyCommits()
		if err != nil {
			t.Fatalf("LocalOnlyCommits: %v", err)
		}
		if n != 2 {
			t.Fatalf("local-only = %d, want 2", n)
		}
	})

	t.Run("a tag does NOT count as a remote, but does count as a harbour", func(t *testing.T) {
		// The discriminating pair: same repo, one tag, two different questions.
		run(t, repo, "tag", "keep", "feat~1")
		defer run(t, repo, "tag", "-d", "keep")
		n, err := g.LocalOnlyCommits()
		if err != nil {
			t.Fatal(err)
		}
		m, err := g.CommitsOnNoRemoteOrTag()
		if err != nil {
			t.Fatal(err)
		}
		if n != 2 || m != 1 {
			t.Fatalf("local-only = %d (want 2), on-no-remote-or-tag = %d (want 1)", n, m)
		}
	})

	t.Run("after a push the count is 0 -- the positive control", func(t *testing.T) {
		run(t, repo, "push", "-q", "origin", "feat")
		n, err := g.LocalOnlyCommits()
		if err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("local-only after push = %d, want 0", n)
		}
	})

	t.Run("a missing branch is an ERROR, not 0", func(t *testing.T) {
		// ⛔ THE COLLAPSE THIS GUARDS. rev-list on a ref that is not there must
		// not render as "nothing unpushed" -- that is the reading a kill would
		// act on.
		gone := &GitWorktree{repoPath: repo, branchName: "does-not-exist"}
		if _, err := gone.LocalOnlyCommits(); err == nil {
			t.Fatal("a missing branch counted as 0 local-only commits; it must refuse")
		}
	})

	t.Run("a repository that cannot be read is an ERROR, not 0", func(t *testing.T) {
		broken := &GitWorktree{repoPath: filepath.Join(t.TempDir(), "nope"), branchName: "feat"}
		if _, err := broken.LocalOnlyCommits(); err == nil {
			t.Fatal("an unreadable repo counted as 0 local-only commits; it must refuse")
		}
	})
}

func TestBranchExists(t *testing.T) {
	repo := reachabilityRepo(t)

	if ok, err := (&GitWorktree{repoPath: repo, branchName: "feat"}).BranchExists(); err != nil || !ok {
		t.Fatalf("feat should exist: ok=%v err=%v", ok, err)
	}
	if ok, err := (&GitWorktree{repoPath: repo, branchName: "nope"}).BranchExists(); err != nil || ok {
		t.Fatalf("nope should be a measured absence: ok=%v err=%v", ok, err)
	}
	// The third state: could not look. It must NOT come back as (false, nil),
	// because that is indistinguishable from "measured absent".
	if _, err := (&GitWorktree{repoPath: filepath.Join(t.TempDir(), "x"), branchName: "feat"}).BranchExists(); err == nil {
		t.Fatal("an unreadable repo reported a measured absence instead of an error")
	}
}
