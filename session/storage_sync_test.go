package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"claude-squad/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// realStorage returns a Storage backed by the REAL config.State against a
// temporary home directory.
//
// It deliberately does not use a fake. An earlier attempt at this merge passed
// six tests against a fake whose GetInstances returned whatever had been seeded
// -- i.e. a fake that behaved like a fresh disk read. The real State returns an
// in-memory field loaded once, so the fake encoded the assumption under test and
// the suite measured nothing. Only the real type can catch that.
func realStorage(t *testing.T) *Storage {
	t.Helper()
	t.Setenv("HOME", t.TempDir())

	s, err := NewStorage(config.LoadState())
	require.NoError(t, err)
	return s
}

// writeStateFileDirectly simulates a second writer: another interface, or the
// `new` subcommand, appending to the state file behind this process's back.
func writeStateFileDirectly(t *testing.T, data ...InstanceData) {
	t.Helper()
	dir, err := config.GetConfigDir()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(dir, 0o755))

	raw, err := json.Marshal(data)
	require.NoError(t, err)

	state := config.LoadState()
	require.NoError(t, state.SaveInstances(raw))
}

// titlesOnDisk reads the state FILE, bypassing any in-memory copy.
func titlesOnDisk(t *testing.T) []string {
	t.Helper()
	dir, err := config.GetConfigDir()
	require.NoError(t, err)

	raw, err := os.ReadFile(filepath.Join(dir, config.StateFileName))
	require.NoError(t, err)

	var wrapper struct {
		Instances json.RawMessage `json:"instances"`
	}
	require.NoError(t, json.Unmarshal(raw, &wrapper))
	if len(wrapper.Instances) == 0 {
		return nil
	}

	var data []InstanceData
	require.NoError(t, json.Unmarshal(wrapper.Instances, &data))
	titles := make([]string, 0, len(data))
	for _, d := range data {
		titles = append(titles, d.Title)
	}
	return titles
}

func started(title, branch string) *Instance {
	return &Instance{Title: title, Branch: branch, Status: Paused, started: true}
}

func TestSyncInstancesAgainstRealState(t *testing.T) {
	t.Run("keeps a session a second writer added after this process loaded", func(t *testing.T) {
		s := realStorage(t) // loads state ONCE, as the interface does at startup

		// Second writer appends while this process holds its own view.
		writeStateFileDirectly(t, InstanceData{Title: "fromCLI", Status: Paused})

		require.NoError(t, s.SyncInstances([]*Instance{started("mine", "b1")}))

		assert.ElementsMatch(t, []string{"mine", "fromCLI"}, titlesOnDisk(t),
			"a session created by another writer must survive this process's save")
	})

	t.Run("MUTATION CONTROL: SaveInstances loses it", func(t *testing.T) {
		// Proves the test above can fail. If this ever reports both titles, the
		// test above has stopped measuring anything.
		s := realStorage(t)
		writeStateFileDirectly(t, InstanceData{Title: "fromCLI", Status: Paused})

		require.NoError(t, s.SaveInstances([]*Instance{started("mine", "b1")}))

		assert.Equal(t, []string{"mine"}, titlesOnDisk(t),
			"SaveInstances is verbatim by design -- DeleteInstance depends on it")
	})

	t.Run("the caller's copy wins for a title it holds", func(t *testing.T) {
		s := realStorage(t)
		writeStateFileDirectly(t, InstanceData{Title: "shared", Branch: "stale", Status: Paused})

		require.NoError(t, s.SyncInstances([]*Instance{started("shared", "fresh")}))

		assert.Equal(t, []string{"shared"}, titlesOnDisk(t), "no duplicate for one title")
	})

	t.Run("unstarted instances are skipped, as SaveInstances does", func(t *testing.T) {
		s := realStorage(t)

		require.NoError(t, s.SyncInstances([]*Instance{{Title: "half-built", Status: Paused}}))

		assert.Empty(t, titlesOnDisk(t))
	})

	t.Run("empty state is not an error", func(t *testing.T) {
		s := realStorage(t)

		require.NoError(t, s.SyncInstances([]*Instance{started("first", "b1")}))

		assert.Equal(t, []string{"first"}, titlesOnDisk(t))
	})

	t.Run("REGRESSION: merging inside SaveInstances would resurrect deletions", func(t *testing.T) {
		// DeleteInstance computes the exact list it wants and hands it to
		// SaveInstances. If that merged with disk the deleted entry would return,
		// which is why the merge lives in SyncInstances instead.
		// Seeded THROUGH this Storage, not behind its back: DeleteInstance reads
		// via s.state, which is an in-memory copy, so an external write would not
		// be visible to it. That staleness is real but it is a separate defect --
		// this guard is about SaveInstances staying verbatim.
		s := realStorage(t)
		require.NoError(t, s.SaveInstances([]*Instance{
			started("keep", "b1"),
			started("drop", "b2"),
		}))

		require.NoError(t, s.DeleteInstance("drop"))

		assert.Equal(t, []string{"keep"}, titlesOnDisk(t),
			"delete must remove the entry, not merge it back")
	})
}

// loadedStorage returns a Storage that has READ the state file, the way the
// interface does at startup.
//
// ⚠ The order is load-bearing. config.LoadState() caches the file's contents in
// an in-memory field, so a Storage built before the seed would hold an empty
// view and LoadInstances would observe nothing -- the test would then pass for
// the wrong reason. Seed first, construct second.
func loadedStorage(t *testing.T, seed ...InstanceData) *Storage {
	t.Helper()
	t.Setenv("HOME", t.TempDir())

	if len(seed) > 0 {
		writeStateFileDirectly(t, seed...)
	}

	s, err := NewStorage(config.LoadState())
	require.NoError(t, err)

	_, err = s.LoadInstances()
	require.NoError(t, err)
	return s
}

// TestSyncInstancesDoesNotResurrectDeletions is Rakizi/the-lab#29.
//
// `claude-squad kill` prunes the state entry correctly. The entry came back
// because the interface then saved its own in-memory list, and the add-only
// merge could not tell a title the caller CREATED from one another writer
// DELETED -- both are "held by the caller, absent from disk".
func TestSyncInstancesDoesNotResurrectDeletions(t *testing.T) {
	t.Run("drops a title another writer deleted, keeps the untouched and the new one", func(t *testing.T) {
		// The interface starts holding two sessions.
		s := loadedStorage(t,
			InstanceData{Title: "untouched", Status: Paused},
			InstanceData{Title: "killed", Status: Paused},
		)

		// `kill killed --yes` runs in another process and prunes the entry.
		writeStateFileDirectly(t, InstanceData{Title: "untouched", Status: Paused})

		// The interface creates a third session and then saves -- on quit, or
		// after any successful start (app.go:385, 475, 508).
		require.NoError(t, s.SyncInstances([]*Instance{
			started("untouched", "b1"),
			started("killed", "b2"),
			started("brandnew", "b3"),
		}))

		got := titlesOnDisk(t)

		// ⛔ ALL THREE ASSERTIONS ARE LOAD-BEARING. The first alone passes for an
		// implementation that drops every held title; the second and third are the
		// discrimination control that says it dropped the right one.
		assert.NotContains(t, got, "killed",
			"a session another writer deleted must not be written back")
		assert.Contains(t, got, "untouched",
			"DISCRIMINATION: a session still on disk must survive -- a merge that "+
				"dropped everything would satisfy the assertion above on its own")
		assert.Contains(t, got, "brandnew",
			"DISCRIMINATION: a session this process created and has never saved is "+
				"also absent from disk, and must NOT be read as a deletion")
		assert.ElementsMatch(t, []string{"untouched", "brandnew"}, got)
	})

	t.Run("MUTATION CONTROL: a Storage that never read the file resurrects it", func(t *testing.T) {
		// Same inputs, one difference: this Storage never observed the file, so
		// seenOnDisk is nil and it CANNOT tell a deletion from a creation. It
		// keeps everything -- the pre-fix behaviour, and the bug in #29.
		//
		// This is the negative half. If it ever reports "killed" as absent, the
		// test above has stopped measuring the fix and is passing on something else.
		t.Setenv("HOME", t.TempDir())
		writeStateFileDirectly(t, InstanceData{Title: "untouched", Status: Paused})

		s, err := NewStorage(config.LoadState())
		require.NoError(t, err)
		require.Nil(t, s.seenOnDisk, "this Storage must not have observed the file")

		require.NoError(t, s.SyncInstances([]*Instance{
			started("untouched", "b1"),
			started("killed", "b2"),
		}))

		assert.Contains(t, titlesOnDisk(t), "killed",
			"COULD NOT LOOK: with no observation to compare against, dropping a title "+
				"would be a guess -- so it is kept, and the test above proves the "+
				"observation is what does the work")
	})

	t.Run("COULD NOT LOOK: an unparseable state file drops nothing", func(t *testing.T) {
		s := loadedStorage(t,
			InstanceData{Title: "untouched", Status: Paused},
			InstanceData{Title: "killed", Status: Paused},
		)

		// The file becomes unreadable between the load and the save. LoadState
		// would report this as an empty instance list -- indistinguishable from
		// "everything was deleted" -- and every held session would be dropped.
		dir, err := config.GetConfigDir()
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(
			filepath.Join(dir, config.StateFileName), []byte("{not json"), 0o644))

		require.NoError(t, s.SyncInstances([]*Instance{
			started("untouched", "b1"),
			started("killed", "b2"),
		}))

		assert.ElementsMatch(t, []string{"untouched", "killed"}, titlesOnDisk(t),
			"a read that FAILED is not a read that found nothing")
	})

	t.Run("the interface's own delete still works through DeleteInstance", func(t *testing.T) {
		// The D key path: ui/list.go drops the row, app.go:874 calls
		// DeleteInstance, then SyncInstances runs with the shortened list. The
		// deleted title is in seenOnDisk and in neither the caller's list nor the
		// file, so it is simply not written.
		s := loadedStorage(t,
			InstanceData{Title: "keep", Status: Paused},
			InstanceData{Title: "gone", Status: Paused},
		)
		require.NoError(t, s.DeleteInstance("gone"))
		require.NoError(t, s.SyncInstances([]*Instance{started("keep", "b1")}))

		assert.Equal(t, []string{"keep"}, titlesOnDisk(t))
	})

	t.Run("a second writer's ADDITION still survives -- the original guarantee", func(t *testing.T) {
		s := loadedStorage(t, InstanceData{Title: "mine", Status: Paused})

		// Another writer appends while this process holds its view.
		writeStateFileDirectly(t,
			InstanceData{Title: "mine", Status: Paused},
			InstanceData{Title: "fromCLI", Status: Paused},
		)

		require.NoError(t, s.SyncInstances([]*Instance{started("mine", "b1")}))

		assert.ElementsMatch(t, []string{"mine", "fromCLI"}, titlesOnDisk(t),
			"the delete discrimination must not cost the add merge SyncInstances exists for")
	})
}
