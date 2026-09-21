package main

import (
	"bytes"
	"claude-squad/log"
	"claude-squad/session"
	"claude-squad/session/git"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// The pre-kill proof. `kill --yes` means "do not ask me", never "do not check"
// (Rakizi/the-lab#30). Before the branch goes with `git branch -D`, two
// independent measurements have to come back clean:
//
//  1. the repository's own refs, REFRESHED FIRST: commits on the branch
//     reachable from no remote-tracking ref and no tag
//     (session/git.CommitsOnNoRemoteOrTag after RefreshRemoteBranch). Always
//     runs; it is the only check that can answer for a paused session, whose
//     worktree is gone by design. The refresh matters: the tracking refs are
//     a cache, and a branch pushed from elsewhere counted as unpushed here
//     until something fetched (PR #3 review K1). A refresh that cannot run
//     is a refusal, because a stale cache can also hide a remote-side delete.
//  2. ops/bin/agent-trace, which also reads the transcript (files written
//     outside the worktree, a decision nobody relayed). Runs when the worktree
//     is on disk. Keyed on its STATE field, not its exit code: exit 1 also
//     fires for DECISION_UNRELAYED and UNFINISHED, which are reasons to finish
//     the session, not reasons a kill would destroy anything.
//
// Every "could not look" is a refusal with exit 3, never a pass. --force skips
// the refusal and RECORDS what it skipped, on stdout, stderr and in the log.

// agentTraceStates that block a kill. Anything else (LANDED, UNFINISHED,
// DECISION_UNRELAYED) proceeds.
const (
	traceLocalOnlyWork = "LOCAL_ONLY_WORK"
	traceCannotTell    = "CANNOT_TELL"
)

// agentTraceTimeout bounds the tool. It searches every repo in the working set
// over gh; measured 3.6s on 2026-09-11. A hang must become could-not-look, not
// a stuck reaper.
const agentTraceTimeout = 120 * time.Second

// agentTraceEnv overrides where the tool is found. Tests point it at a fake.
const agentTraceEnv = "CLAUDE_SQUAD_AGENT_TRACE"

// traceRow is the one row of agent-trace --json that names this session.
type traceRow struct {
	Session  string   `json:"session"`
	Worktree string   `json:"worktree"`
	State    string   `json:"state"`
	Verdict  string   `json:"verdict"`
	Blind    []string `json:"blind"`
}

type traceOutput struct {
	Sessions []traceRow `json:"sessions"`
}

// agentTracePath resolves the tool: the env override, then PATH, then the
// estate's fixed location. Not found is a could-not-look, reported as such.
func agentTracePath() (string, error) {
	if p := os.Getenv(agentTraceEnv); p != "" {
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("%s=%s: %w", agentTraceEnv, p, err)
		}
		return p, nil
	}
	if p, err := exec.LookPath("agent-trace"); err == nil {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("agent-trace not on PATH and home dir unknown: %w", err)
	}
	p := filepath.Join(home, "the-lab", "ops", "bin", "agent-trace")
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("agent-trace not on PATH and not at %s", p)
	}
	return p, nil
}

// runAgentTrace runs `agent-trace <title> --json` and returns the row for this
// session. An error means COULD NOT LOOK: the tool is missing, timed out,
// printed no JSON, produced no row for the title, or traced a different
// worktree than the one state names (its lookup falls back to a substring
// match, so "w-nag-30" can resolve to w-nag-307's tree). A state entry that
// records NO worktree path cannot have its row verified at all, so it is
// refused rather than waved through -- the check is unconditional.
func runAgentTrace(title, worktreePath string) (*traceRow, error) {
	if worktreePath == "" {
		return nil, fmt.Errorf("state records no worktree path for %q, so an agent-trace row cannot be verified as this session's", title)
	}
	tool, err := agentTracePath()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), agentTraceTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, tool, title, "--json")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("agent-trace did not finish within %s", agentTraceTimeout)
	}

	// Parse before judging the exit code: the JSON carries the state, and the
	// exit code is 1 for states that do not block a kill.
	var out traceOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		msg := strings.TrimSpace(stdout.String() + "\n" + stderr.String())
		if runErr != nil {
			return nil, fmt.Errorf("agent-trace failed (%v) and printed no JSON: %s", runErr, msg)
		}
		return nil, fmt.Errorf("agent-trace printed no JSON: %s", msg)
	}
	for i := range out.Sessions {
		r := &out.Sessions[i]
		if r.Session != title {
			continue
		}
		if r.Worktree != "" && r.Worktree != worktreePath {
			return nil, fmt.Errorf("agent-trace traced %s, not this session's worktree %s",
				r.Worktree, worktreePath)
		}
		return r, nil
	}
	return nil, fmt.Errorf("agent-trace produced no row for %q", title)
}

// traceProceedStates are the agent-trace states a kill may proceed on. The
// vocabulary is CLOSED: a state this build does not know -- an empty one, a
// renamed one, one from a different agent-trace earlier on PATH -- is a
// could-not-look, never a pass. Without this list the switch below fell
// through to "proceed" on any unrecognised word, in front of `git branch -D`
// (PR #3 review 5194349086 §4).
var traceProceedStates = map[string]bool{"LANDED": true, "UNFINISHED": true, "DECISION_UNRELAYED": true}

// judgeTrace turns an agent-trace row (or the failure to get one) into the kill
// decision. nil means proceed.
func judgeTrace(row *traceRow, lookErr error) error {
	if lookErr != nil {
		return couldNotLook("pre-kill check could not run: %v", lookErr)
	}
	switch {
	case row.State == traceLocalOnlyWork:
		return refused("agent-trace: %s -- %s", row.State, row.Verdict)
	case row.State == traceCannotTell:
		blind := strings.Join(row.Blind, "; ")
		if blind == "" {
			blind = row.Verdict
		}
		return couldNotLook("agent-trace could not tell whether a kill loses work: %s", blind)
	case traceProceedStates[row.State]:
		return nil
	default:
		return couldNotLook("agent-trace reported state %q, which this build does not know; not proceeding on it", row.State)
	}
}

// judgeLocalOnly turns the refs-based count into the kill decision. nil means
// proceed. An error counting is a refusal: a blind count is not a zero. The
// count is CommitsOnNoRemoteOrTag, so the remedy printed here is one that
// clears it: either a push or a tag makes the number drop.
func judgeLocalOnly(n int, countErr error) error {
	if countErr != nil {
		return couldNotLook("could not count local-only commits: %v", countErr)
	}
	if n > 0 {
		return refused("%d commit(s) on the branch exist on no remote and no tag. Push or tag them first.", n)
	}
	return nil
}

// freshLocalOnly refreshes the remote-tracking refs for the branch and then
// counts. A refresh that fails is returned as the count's error, so the
// caller's could-not-look path fires: the cached view may hide a push OR a
// remote-side delete, and neither direction is safe to guess.
//
// ⛔ THE BRANCH IS PROVEN TO EXIST FIRST, AND A GONE BRANCH IS NOT AN ERROR.
// `CommitsOnNoRemoteOrTag` runs `rev-list refs/heads/<b>`, which on an absent
// ref exits 128 ("ambiguous argument") -- correctly NOT rendered as 0 by that
// function, so it surfaced here as could-not-look and `cs kill` refused. But a
// branch that is not there cannot be holding unpushed work: `git branch -D`
// has nothing left to make unreachable, which is the only question this check
// asks. Refusing was guarding nothing and cost the reap path instead.
//
// MEASURED 2026-09-21: 11 of 25 failed reaps were exactly this -- "ambiguous
// argument 'refs/heads/rakizi/w-nag-XXXX'" on sessions whose worktree AND
// branch were already gone, with the work safely on origin (spot-checked
// w-nag-1127, w-nag-1155, w-nag-1121: local branch absent,
// refs/remotes/origin/<b> present). Those slots could not be freed at all, and
// dispatch reported "36/16 workers live -- no free slot".
//
// ⭐ THE SHAPE IS ALREADY SOLVED NEXT DOOR: prePauseProof asks BranchExists
// before counting, because a pause needs the branch to survive. A kill needs
// the opposite -- a gone branch is the safest case there is -- so the same
// probe is read to the opposite conclusion here, and an ERROR from it stays a
// refusal in both.
func freshLocalOnly(wt *git.GitWorktree) (int, error) {
	exists, err := wt.BranchExists()
	if err != nil {
		// ⛔ NOT an absence. A probe that could not answer is a refusal, per
		// BranchExists' own three-outcome contract (Rakizi/the-lab#30).
		return 0, fmt.Errorf("could not check whether branch %s still exists: %w",
			wt.GetBranchName(), err)
	}
	if !exists {
		// Nothing to lose: no ref, so `git branch -D` unreachables nothing.
		return 0, nil
	}
	if err := wt.RefreshRemoteBranch(); err != nil {
		return 0, err
	}
	return wt.CommitsOnNoRemoteOrTag()
}

// preKillProof is what killInstance runs before target.Kill(). It returns the
// first reason NOT to proceed, or nil. The caller decides what --force does
// with that reason; this function never consults the flag.
func preKillProof(target *session.Instance, title, worktreePath string) error {
	wt, err := target.GetGitWorktree()
	if err != nil {
		return couldNotLook("no git worktree on %q: %v", title, err)
	}
	if err := judgeLocalOnly(freshLocalOnly(wt)); err != nil {
		return err
	}

	// A paused session has no worktree on disk by design; agent-trace reads
	// nothing without one. The refs count above already answered for it, so
	// its blindness here is expected rather than a failed check. An EMPTY
	// recorded path is not that case: it falls through to runAgentTrace,
	// which refuses it, because a row could not be verified as this
	// session's.
	if worktreePath != "" {
		if _, statErr := os.Stat(worktreePath); statErr != nil && os.IsNotExist(statErr) {
			return nil
		}
	}
	row, lookErr := runAgentTrace(title, worktreePath)
	if err := judgeTrace(row, lookErr); err != nil {
		return err
	}
	if row.State != "" {
		fmt.Fprintf(os.Stderr, "%s\tagent-trace: %s\n", title, row.State)
	}
	return nil
}

// recordForcedKill writes the override everywhere a reader might look later:
// the log file, stderr, and the note the caller appends to stdout. A forced
// kill that leaves no trace is indistinguishable from one that passed.
func recordForcedKill(title string, reason error) string {
	var exitErr exitError
	code := exitUsage
	if errors.As(reason, &exitErr) {
		code = exitErr.code
	}
	note := fmt.Sprintf("FORCED past exit %d: %v", code, reason)
	log.WarningLog.Printf("kill %q %s", title, note)
	fmt.Fprintf(os.Stderr, "%s\t%s\n", title, note)
	return note
}
