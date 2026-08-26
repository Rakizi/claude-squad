package main

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExitCodeFor(t *testing.T) {
	t.Run("no error is success", func(t *testing.T) {
		assert.Equal(t, exitOK, exitCodeFor(nil))
	})

	t.Run("a plain error is a usage failure", func(t *testing.T) {
		// cobra's own argument errors arrive unwrapped and must still behave.
		assert.Equal(t, exitUsage, exitCodeFor(errors.New("boom")))
	})

	t.Run("refused and could-not-look are DISTINGUISHABLE", func(t *testing.T) {
		// The whole point. A caller must be able to tell "declined, try another
		// argument" from "I could not tell, do not conclude anything" without
		// reading the message.
		declined := exitCodeFor(refused("already exists"))
		unknown := exitCodeFor(couldNotLook("state unreadable"))

		assert.Equal(t, exitRefused, declined)
		assert.Equal(t, exitCouldNotLook, unknown)
		assert.NotEqual(t, declined, unknown,
			"collapsing these is what makes a script read 'could not check' as 'not there'")
	})

	t.Run("every code is distinct", func(t *testing.T) {
		seen := map[int]string{}
		for name, code := range map[string]int{
			"ok": exitOK, "usage": exitUsage,
			"refused": exitRefused, "couldNotLook": exitCouldNotLook,
		} {
			if prev, dup := seen[code]; dup {
				t.Fatalf("%s and %s share exit code %d", name, prev, code)
			}
			seen[code] = name
		}
	})

	t.Run("the message survives wrapping", func(t *testing.T) {
		err := refused("a session named %q already exists", "foo")

		assert.Contains(t, err.Error(), `"foo"`)
	})

	t.Run("a wrapped cause is still reachable", func(t *testing.T) {
		cause := errors.New("underlying")
		wrapped := exitError{code: exitCouldNotLook, err: fmt.Errorf("outer: %w", cause)}

		assert.ErrorIs(t, wrapped, cause, "errors.Is must see through the exit code")
		assert.Equal(t, exitCouldNotLook, exitCodeFor(wrapped))
	})
}
