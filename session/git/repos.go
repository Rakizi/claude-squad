package git

import (
	"os"
	"path/filepath"
	"strings"
)

// IsRepo reports whether dir is the root of a git repository.
//
// It looks for a .git entry rather than shelling out, and accepts a file as
// well as a directory: a worktree's .git is a file pointing at the real one.
func IsRepo(dir string) bool {
	if dir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// DiscoverRepos returns the repositories a new session may be created in: the
// working directory first when it is one, then every repository found ONE level
// below each configured root.
//
// The working directory leads so that an unconfigured install behaves exactly
// as it did before this existed -- one candidate, the current directory.
//
// Order is stable and duplicates are dropped, so an index into the result is a
// usable selection that does not move between calls. A root that cannot be read
// is skipped rather than reported: a stale entry in a config file should not
// stop the application starting.
func DiscoverRepos(roots []string, cwd string) []string {
	var out []string
	seen := make(map[string]struct{})

	add := func(dir string) {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return
		}
		if !IsRepo(abs) {
			return
		}
		if _, dup := seen[abs]; dup {
			return
		}
		seen[abs] = struct{}{}
		out = append(out, abs)
	}

	add(cwd)

	for _, root := range roots {
		root = expandHome(strings.TrimSpace(root))
		if root == "" {
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			add(filepath.Join(root, e.Name()))
		}
	}

	return out
}

// expandHome resolves a leading ~ so hand-edited config paths work. Anything
// else is returned unchanged, including a bare ~ that cannot be resolved.
func expandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if p == "~" {
		return home
	}
	return filepath.Join(home, p[2:])
}
