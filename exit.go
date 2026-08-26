package main

import "fmt"

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
func exitCodeFor(err error) int {
	if err == nil {
		return exitOK
	}
	type coder interface{ ExitCode() int }
	if c, ok := err.(coder); ok {
		return c.ExitCode()
	}
	return exitUsage
}
