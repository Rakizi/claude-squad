package git

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mkRepo makes dir look like a repository root. DiscoverRepos only tests for a
// .git entry, so this stays cheap and does not need a real repository.
func mkRepo(t *testing.T, dir string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))
	return dir
}

func TestDiscoverRepos(t *testing.T) {
	t.Run("no roots configured yields only the working directory", func(t *testing.T) {
		cwd := mkRepo(t, filepath.Join(t.TempDir(), "here"))

		got := DiscoverRepos(nil, cwd)

		assert.Equal(t, []string{cwd}, got,
			"an unconfigured install must behave exactly as it did before this existed")
	})

	t.Run("a working directory that is not a repo yields nothing", func(t *testing.T) {
		notARepo := t.TempDir()

		assert.Empty(t, DiscoverRepos(nil, notARepo))
	})

	t.Run("finds repos one level below a root", func(t *testing.T) {
		root := t.TempDir()
		a := mkRepo(t, filepath.Join(root, "alpha"))
		b := mkRepo(t, filepath.Join(root, "beta"))
		require.NoError(t, os.MkdirAll(filepath.Join(root, "not-a-repo"), 0o755))

		got := DiscoverRepos([]string{root}, "")

		assert.ElementsMatch(t, []string{a, b}, got)
	})

	t.Run("does NOT descend two levels", func(t *testing.T) {
		root := t.TempDir()
		deep := mkRepo(t, filepath.Join(root, "outer", "inner"))

		got := DiscoverRepos([]string{root}, "")

		assert.NotContains(t, got, deep,
			"scanning deeply would walk an entire home directory")
	})

	t.Run("the working directory leads, and is not duplicated by a root", func(t *testing.T) {
		root := t.TempDir()
		cwd := mkRepo(t, filepath.Join(root, "alpha"))
		other := mkRepo(t, filepath.Join(root, "beta"))

		got := DiscoverRepos([]string{root}, cwd)

		require.Len(t, got, 2, "alpha is both the cwd and under the root: one entry, not two")
		assert.Equal(t, cwd, got[0], "the working directory must come first")
		assert.Contains(t, got, other)
	})

	t.Run("an unreadable root is skipped, not fatal", func(t *testing.T) {
		root := t.TempDir()
		good := mkRepo(t, filepath.Join(root, "alpha"))

		got := DiscoverRepos([]string{filepath.Join(root, "does-not-exist"), root}, "")

		assert.Equal(t, []string{good}, got,
			"a stale config entry must not stop the application starting")
	})

	t.Run("hidden directories are ignored", func(t *testing.T) {
		root := t.TempDir()
		mkRepo(t, filepath.Join(root, ".cache"))

		assert.Empty(t, DiscoverRepos([]string{root}, ""))
	})

	t.Run("order is stable across calls", func(t *testing.T) {
		root := t.TempDir()
		for _, n := range []string{"c", "a", "b"} {
			mkRepo(t, filepath.Join(root, n))
		}

		first := DiscoverRepos([]string{root}, "")
		for i := 0; i < 5; i++ {
			assert.Equal(t, first, DiscoverRepos([]string{root}, ""),
				"an index into this list is a selection; it must not move")
		}
	})
}

func TestIsRepo(t *testing.T) {
	t.Run("directory with .git", func(t *testing.T) {
		assert.True(t, IsRepo(mkRepo(t, filepath.Join(t.TempDir(), "r"))))
	})

	t.Run("a .git FILE counts -- that is what a worktree has", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /elsewhere\n"), 0o644))

		assert.True(t, IsRepo(dir))
	})

	t.Run("plain directory", func(t *testing.T) {
		assert.False(t, IsRepo(t.TempDir()))
	})

	t.Run("empty string", func(t *testing.T) {
		assert.False(t, IsRepo(""))
	})
}
