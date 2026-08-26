package session

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeState is an in-memory config.InstanceStorage. It stands in for the state
// file so these tests never touch disk, tmux or git.
type fakeState struct {
	raw json.RawMessage
}

func (f *fakeState) SaveInstances(j json.RawMessage) error { f.raw = j; return nil }
func (f *fakeState) GetInstances() json.RawMessage         { return f.raw }
func (f *fakeState) DeleteAllInstances() error             { f.raw = nil; return nil }

// storedTitles reads the titles straight out of the fake state, without going
// through LoadInstances -- that would call FromInstanceData, which restores a
// tmux session per entry.
func storedTitles(t *testing.T, f *fakeState) []string {
	t.Helper()
	if len(f.raw) == 0 {
		return nil
	}
	var data []InstanceData
	require.NoError(t, json.Unmarshal(f.raw, &data))
	titles := make([]string, 0, len(data))
	for _, d := range data {
		titles = append(titles, d.Title)
	}
	return titles
}

// seed writes instance data directly, as a second writer would.
func seed(t *testing.T, f *fakeState, data ...InstanceData) {
	t.Helper()
	raw, err := json.Marshal(data)
	require.NoError(t, err)
	f.raw = raw
}

func startedInstance(title, branch string) *Instance {
	return &Instance{Title: title, Branch: branch, Status: Paused, started: true}
}

func TestSyncInstances(t *testing.T) {
	t.Run("keeps an instance another writer added", func(t *testing.T) {
		f := &fakeState{}
		s, err := NewStorage(f)
		require.NoError(t, err)

		// A second writer -- another TUI, or the CLI -- has stored "outsider".
		seed(t, f, InstanceData{Title: "outsider"})

		// This process only ever knew about "mine".
		require.NoError(t, s.SyncInstances([]*Instance{startedInstance("mine", "b1")}))

		assert.ElementsMatch(t, []string{"mine", "outsider"}, storedTitles(t, f),
			"an instance this process never saw must survive its save")
	})

	t.Run("MUTATION CONTROL: SaveInstances loses it", func(t *testing.T) {
		// Proves the test above can actually detect the bug. If this ever starts
		// passing with both titles, the test above is no longer measuring anything.
		f := &fakeState{}
		s, err := NewStorage(f)
		require.NoError(t, err)

		seed(t, f, InstanceData{Title: "outsider"})
		require.NoError(t, s.SaveInstances([]*Instance{startedInstance("mine", "b1")}))

		assert.Equal(t, []string{"mine"}, storedTitles(t, f),
			"SaveInstances is verbatim by design -- DeleteInstance depends on it")
	})

	t.Run("the caller's copy wins for titles it holds", func(t *testing.T) {
		f := &fakeState{}
		s, err := NewStorage(f)
		require.NoError(t, err)

		seed(t, f, InstanceData{Title: "shared", Branch: "stale"})
		require.NoError(t, s.SyncInstances([]*Instance{startedInstance("shared", "fresh")}))

		var data []InstanceData
		require.NoError(t, json.Unmarshal(f.raw, &data))
		require.Len(t, data, 1, "no duplicate entry for the same title")
		assert.Equal(t, "fresh", data[0].Branch)
	})

	t.Run("unstarted instances are skipped, as SaveInstances does", func(t *testing.T) {
		f := &fakeState{}
		s, err := NewStorage(f)
		require.NoError(t, err)

		notStarted := &Instance{Title: "half-built", Status: Paused}
		require.NoError(t, s.SyncInstances([]*Instance{notStarted}))

		assert.Empty(t, storedTitles(t, f))
	})

	t.Run("empty state is not an error", func(t *testing.T) {
		f := &fakeState{}
		s, err := NewStorage(f)
		require.NoError(t, err)

		require.NoError(t, s.SyncInstances([]*Instance{startedInstance("first", "b1")}))
		assert.Equal(t, []string{"first"}, storedTitles(t, f))
	})

	t.Run("REGRESSION: a merge in SaveInstances would resurrect deletions", func(t *testing.T) {
		// DeleteInstance computes the exact list it wants and hands it to
		// SaveInstances. If that merged with what is on disk, the deleted entry
		// would come straight back. This is why the merge lives in SyncInstances.
		f := &fakeState{}
		s, err := NewStorage(f)
		require.NoError(t, err)

		seed(t,
			f,
			InstanceData{Title: "keep", Status: Paused},
			InstanceData{Title: "drop", Status: Paused},
		)

		require.NoError(t, s.DeleteInstance("drop"))
		assert.Equal(t, []string{"keep"}, storedTitles(t, f),
			"delete must remove the entry, not merge it back")
	})
}
