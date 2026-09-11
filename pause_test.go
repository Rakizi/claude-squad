package main

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeProver answers the two questions prePauseProof asks, with a settable
// third state (an error) for each.
type fakeProver struct {
	exists    bool
	existsErr error
	unreached int
	countErr  error
}

func (f fakeProver) BranchExists() (bool, error)          { return f.exists, f.existsErr }
func (f fakeProver) CommitsOnNoRemoteOrTag() (int, error) { return f.unreached, f.countErr }
func (f fakeProver) GetBranchName() string                { return "rakizi/w-x-1" }

func TestPrePauseProof(t *testing.T) {
	t.Run("branch exists and is on a remote: pause, silently", func(t *testing.T) {
		var w bytes.Buffer
		require.NoError(t, prePauseProof(fakeProver{exists: true}, "w-x-1", &w))
		assert.Empty(t, w.String(), "a clean pause must not warn")
	})

	t.Run("branch MISSING is refused, exit 2 -- a pause that could never resume", func(t *testing.T) {
		// ⛔ THE LOAD-BEARING HALF. Paused promises "resumable from the kept
		// branch"; with no branch, ⏸ would be a lie.
		var w bytes.Buffer
		err := prePauseProof(fakeProver{exists: false}, "w-x-1", &w)
		require.Error(t, err)
		assert.Equal(t, exitRefused, exitCodeFor(err))
		assert.Contains(t, err.Error(), "does not exist")
	})

	t.Run("branch check that could NOT run is exit 3, never a pass", func(t *testing.T) {
		var w bytes.Buffer
		err := prePauseProof(fakeProver{existsErr: errors.New("not a git repository")}, "w-x-1", &w)
		require.Error(t, err)
		assert.Equal(t, exitCouldNotLook, exitCodeFor(err))
	})

	t.Run("reachability count that could NOT run is exit 3, never a pass", func(t *testing.T) {
		var w bytes.Buffer
		err := prePauseProof(fakeProver{exists: true, countErr: errors.New("rev-list failed")}, "w-x-1", &w)
		require.Error(t, err)
		assert.Equal(t, exitCouldNotLook, exitCodeFor(err))
	})

	t.Run("commits on no remote or tag: pause proceeds, and says so plainly with the count", func(t *testing.T) {
		// The ticket's words for pause are "say plainly that resume may fail",
		// against kill's "refuse, do not warn". The branch is kept, so nothing
		// is destroyed -- but the one place the work exists is named.
		var w bytes.Buffer
		require.NoError(t, prePauseProof(fakeProver{exists: true, unreached: 3}, "w-x-1", &w))
		assert.Contains(t, w.String(), "3 commit(s)")
		assert.Contains(t, w.String(), "rakizi/w-x-1")
		assert.Contains(t, w.String(), "resume will fail")
	})

	t.Run("the two refusals carry DIFFERENT codes", func(t *testing.T) {
		var w bytes.Buffer
		missing := exitCodeFor(prePauseProof(fakeProver{exists: false}, "w-x-1", &w))
		blind := exitCodeFor(prePauseProof(fakeProver{existsErr: errors.New("x")}, "w-x-1", &w))
		assert.NotEqual(t, missing, blind)
	})
}
