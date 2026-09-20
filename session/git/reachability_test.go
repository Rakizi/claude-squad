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

func count(t *testing.T, g *GitWorktree) int {
	t.Helper()
	n, err := g.CommitsOnNoRemoteOrTag()
	if err != nil {
		t.Fatalf("CommitsOnNoRemoteOrTag: %v", err)
	}
	return n
}

func TestCommitsOnNoRemoteOrTag(t *testing.T) {
	repo := reachabilityRepo(t)
	g := &GitWorktree{repoPath: repo, branchName: "feat"}

	t.Run("two commits pushed nowhere and tagged nowhere count as 2", func(t *testing.T) {
		if n := count(t, g); n != 2 {
			t.Fatalf("count = %d, want 2", n)
		}
	})

	t.Run("a tag IS a harbour: tagging one of them lowers the count to 1", func(t *testing.T) {
		// ⛔ THE REMEDY THE REFUSAL PRINTS. "Push or tag them first" must be
		// cleared by a tag, or the operator does what the message says and
		// gets the same refusal forever (PR #3 review F1).
		run(t, repo, "tag", "keep", "feat~1")
		defer run(t, repo, "tag", "-d", "keep")
		if n := count(t, g); n != 1 {
			t.Fatalf("count with one commit tagged = %d, want 1", n)
		}
	})

	t.Run("after a push the count is 0 -- the positive control", func(t *testing.T) {
		run(t, repo, "push", "-q", "origin", "feat")
		if n := count(t, g); n != 0 {
			t.Fatalf("count after push = %d, want 0", n)
		}
	})

	t.Run("a missing branch is an ERROR, not 0", func(t *testing.T) {
		// ⛔ THE COLLAPSE THIS GUARDS. rev-list on a ref that is not there must
		// not render as "nothing unpushed" -- that is the reading a kill would
		// act on.
		gone := &GitWorktree{repoPath: repo, branchName: "does-not-exist"}
		if _, err := gone.CommitsOnNoRemoteOrTag(); err == nil {
			t.Fatal("a missing branch counted as 0; it must refuse")
		}
	})

	t.Run("a repository that cannot be read is an ERROR, not 0", func(t *testing.T) {
		broken := &GitWorktree{repoPath: filepath.Join(t.TempDir(), "nope"), branchName: "feat"}
		if _, err := broken.CommitsOnNoRemoteOrTag(); err == nil {
			t.Fatal("an unreadable repo counted as 0; it must refuse")
		}
	})
}

// The count reads a cache. These pin that RefreshRemoteBranch turns the cache
// into the truth in BOTH directions, and that a refresh which could not run is
// an error rather than a silent stale read.
func TestRefreshRemoteBranch(t *testing.T) {
	repo := reachabilityRepo(t)
	g := &GitWorktree{repoPath: repo, branchName: "feat"}

	t.Run("pushed elsewhere, never fetched here: 2 before refresh, 0 after", func(t *testing.T) {
		// The deterministic control from PR #3 review K1: the work IS on the
		// remote, this clone just has no tracking ref for it.
		run(t, repo, "push", "-q", "origin", "feat")
		run(t, repo, "update-ref", "-d", "refs/remotes/origin/feat")
		if n := count(t, g); n != 2 {
			t.Fatalf("setup broken: stale view should count 2, got %d", n)
		}
		if err := g.RefreshRemoteBranch(); err != nil {
			t.Fatalf("RefreshRemoteBranch: %v", err)
		}
		if n := count(t, g); n != 0 {
			t.Fatalf("count after refresh = %d, want 0 (the branch is on the remote)", n)
		}
	})

	t.Run("deleted on the remote, tracking ref stale: 0 before refresh, 2 after", func(t *testing.T) {
		// The dangerous direction: a stale tracking ref makes destroyed-on-
		// remote work look safe. The scoped --prune must drop it, and the
		// "couldn't find remote ref" exit that comes with it is an absence, not
		// a failure.
		run(t, repo, "push", "-q", "origin", "--delete", "feat")
		// A push from THIS clone also drops its own tracking ref; the stale
		// state comes from a delete made elsewhere, so re-create the ref the
		// way a never-refetched clone would still hold it.
		run(t, repo, "update-ref", "refs/remotes/origin/feat", "refs/heads/feat")
		if n := count(t, g); n != 0 {
			t.Fatalf("setup broken: stale view should count 0, got %d", n)
		}
		if err := g.RefreshRemoteBranch(); err != nil {
			t.Fatalf("RefreshRemoteBranch on a remote-deleted branch must not error: %v", err)
		}
		if n := count(t, g); n != 2 {
			t.Fatalf("count after refresh = %d, want 2 (the remote no longer has it)", n)
		}
	})

	t.Run("a remote that cannot be reached is an ERROR", func(t *testing.T) {
		run(t, repo, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))
		if err := g.RefreshRemoteBranch(); err == nil {
			t.Fatal("an unreachable remote refreshed silently; the count would be a stale cache read as truth")
		}
	})

	t.Run("no remotes at all: nothing to refresh, no error", func(t *testing.T) {
		run(t, repo, "remote", "remove", "origin")
		if err := g.RefreshRemoteBranch(); err != nil {
			t.Fatalf("a repository with no remotes has nothing to refresh: %v", err)
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
