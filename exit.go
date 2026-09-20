package main

import (
	"errors"
	"fmt"
)

// Exit codes. A caller must be able to tell these apart WITHOUT parsing the
// message, because the three call for different responses:
//
//	refused        the request was understood and declined -- a different
//	               argument may succeed (pick another title, another path)
//	could-not-look the answer is unknown, not negative -- state or tmux could
//	               not be read, so nothing should be concluded and retrying the
//	               same call is unlikely to help
//
// Collapsing them into one code is what makes a script treat "I could not
// check" as "it is not there".
const (
	exitOK           = 0
	exitUsage        = 1
	exitRefused      = 2
	exitCouldNotLook = 3
)

// exitError carries the code a failure should produce.
//
// Errors without one exit as exitUsage, which keeps cobra's own argument
// errors behaving sensibly without every call site having to wrap them.
type exitError struct {
	code int
	err  error
}

func (e exitError) Error() string { return e.err.Error() }
func (e exitError) Unwrap() error { return e.err }
func (e exitError) ExitCode() int { return e.code }

// refused reports a request that was understood and declined.
func refused(format string, a ...any) error {
	return exitError{code: exitRefused, err: fmt.Errorf(format, a...)}
}

// couldNotLook reports that the answer is unknown rather than negative.
func couldNotLook(format string, a ...any) error {
	return exitError{code: exitCouldNotLook, err: fmt.Errorf(format, a...)}
}

// exitCodeFor returns the code an error should exit with.
//
// ⛔ errors.As, NOT a type assertion. A bare type assertion only sees the
// OUTERMOST error, so the moment a caller adds context --
// fmt.Errorf("...: %w", err) -- a couldNotLook stopped being one and came out
// as exitUsage. MEASURED 2026-09-20:
//
//	bare couldNotLook          -> 3
//	the same wrapped with %w   -> 1          <- the collapse
//	⭐ CONTROL, a plain error  -> 1          (so 1 is not proof of anything)
//
// That is this repository's own three-state rule failing inside the function
// that exists to enforce it, and the live path was kill.go's "failed to read
// stored instances" -- a genuine could-not-look reaching a script as "bad
// arguments". Found by the independent review of Rakizi/claude-squad#3;
// exit.go was untouched by that PR.
func exitCodeFor(err error) int {
	if err == nil {
		return exitOK
	}
	type coder interface{ ExitCode() int }
	var c coder
	if errors.As(err, &c) {
		return c.ExitCode()
	}
	return exitUsage
}
