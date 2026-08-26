package overlay

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPickByRune(t *testing.T) {
	profiles := []string{"claude", "review", "nag+tools", "tools+nag"}
	repos := []string{
		"the-lab",
		"the-lab/NextActionGuide",
		"the-lab/NextActionGuide-Tools",
		"the-lab/claude-squad",
		"the-lab/extern-deps",
		"the-lab/lab-plugins",
		"the-lab/ops",
	}

	t.Run("digits address POSITION, and 1 is the first", func(t *testing.T) {
		// The list is printed "1." — a shortcut that disagreed with the number
		// on screen would be worse than no shortcut at all.
		assert.Equal(t, 0, PickByRune(profiles, '1', 0))
		assert.Equal(t, 2, PickByRune(profiles, '3', 0))
		assert.Equal(t, 3, PickByRune(profiles, '4', 0))
	})

	t.Run("a digit past the end selects nothing", func(t *testing.T) {
		assert.Equal(t, -1, PickByRune(profiles, '9', 0))
		assert.Equal(t, -1, PickByRune(nil, '1', 0))
	})

	t.Run("the profile letters are all unique — one press each", func(t *testing.T) {
		assert.Equal(t, 0, PickByRune(profiles, 'c', -1))
		assert.Equal(t, 1, PickByRune(profiles, 'r', -1))
		assert.Equal(t, 2, PickByRune(profiles, 'n', -1))
		assert.Equal(t, 3, PickByRune(profiles, 't', -1))
	})

	t.Run("⛔ a repo label is a PATH — match the LAST segment", func(t *testing.T) {
		// Every repo label starts "the-lab/". Matching the first character would
		// make all seven answer to "t" and the feature would be useless.
		assert.Equal(t, 3, PickByRune(repos, 'c', -1), "claude-squad, not the-lab")
		assert.Equal(t, 4, PickByRune(repos, 'e', -1), "extern-deps")
		assert.Equal(t, 6, PickByRune(repos, 'o', -1), "ops")
		assert.Equal(t, 0, PickByRune(repos, 't', -1), "the-lab itself has no slash")
	})

	t.Run("collisions CYCLE — the measured case", func(t *testing.T) {
		// NextActionGuide and NextActionGuide-Tools both answer to n. Pressing n
		// again must advance rather than sit on the first match, which is what
		// every desktop menu has done for decades.
		first := PickByRune(repos, 'n', -1)
		assert.Equal(t, 1, first)
		second := PickByRune(repos, 'n', first)
		assert.Equal(t, 2, second, "a second press advances to the other N")
		third := PickByRune(repos, 'n', second)
		assert.Equal(t, 1, third, "and it wraps back")
	})

	t.Run("case does not matter", func(t *testing.T) {
		assert.Equal(t, 1, PickByRune(repos, 'N', -1))
		assert.Equal(t, 1, PickByRune(repos, 'n', -1))
	})

	t.Run("an unmatched letter selects nothing", func(t *testing.T) {
		// ⛔ It must NOT fall back to "the first item" or to the current one.
		// A shortcut that silently does something else is worse than one that
		// does nothing, because the reader believes it worked.
		assert.Equal(t, -1, PickByRune(profiles, 'z', -1))
		assert.Equal(t, -1, PickByRune(repos, 'q', 2))
	})

	t.Run("non-letters and non-digits select nothing", func(t *testing.T) {
		assert.Equal(t, -1, PickByRune(profiles, '-', 0))
		assert.Equal(t, -1, PickByRune(profiles, ' ', 0))
		assert.Equal(t, -1, PickByRune(profiles, '0', 0), "0 is not a position")
	})

	t.Run("a nonsense `from` still resolves, just maybe not the next", func(t *testing.T) {
		assert.Equal(t, 1, PickByRune(repos, 'n', 999))
		assert.Equal(t, 1, PickByRune(repos, 'n', -50))
	})
}
