package main

import (
	"claude-squad/config"
	"claude-squad/log"
	"claude-squad/session"
	"claude-squad/session/git"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var (
	newRepo    string
	newProgram string
	newProfile string
)

// resolveProgram decides what command a new session runs.
//
// ⛔ `DefaultProgram` MUST NOT be read directly. It holds a profile's NAME when
// profiles are configured, and only Config.GetProgram resolves that name to a
// command -- reading the field raw would try to execute the literal string. A
// default of "claude" happens to be on PATH and so would appear to work while
// silently dropping every flag the profile carries; a default of "review" would
// fail with a confusing "not found".
func resolveProgram(cfg *config.Config, program, profile string) (string, error) {
	if program != "" && profile != "" {
		return "", refused(
			"--program and --profile both given; they name the same thing two ways.\n" +
				"Pass --profile to use a configured one, or --program for a literal command.")
	}
	if program != "" {
		return program, nil
	}
	if profile == "" {
		return cfg.GetProgram(), nil
	}
	names := make([]string, 0, len(cfg.Profiles))
	for _, p := range cfg.Profiles {
		if p.Name == profile {
			return p.Program, nil
		}
		names = append(names, p.Name)
	}
	// Refused, not could-not-look: the config WAS read, and it says no such
	// profile. A different argument may well succeed, so name the ones that would.
	if len(names) == 0 {
		return "", refused("no profile named %q, and no profiles are configured", profile)
	}
	return "", refused("no profile named %q. Configured: %s", profile, strings.Join(names, ", "))
}

// appendInstance adds an instance to stored state.
//
// It reads and writes the stored JSON directly rather than going through
// Storage.LoadInstances, which calls FromInstanceData and so restores a tmux
// session for every existing entry. Adding one session must not touch the
// others.
func appendInstance(instance *session.Instance) error {
	state := config.LoadState()

	var stored []session.InstanceData
	if raw := state.GetInstances(); len(raw) > 0 {
		if err := json.Unmarshal(raw, &stored); err != nil {
			return couldNotLook("failed to read stored instances: %v", err)
		}
	}

	stored = append(stored, instance.ToInstanceData())
	raw, err := json.Marshal(stored)
	if err != nil {
		return fmt.Errorf("failed to encode instances: %w", err)
	}
	return state.SaveInstances(raw)
}

// titleTaken reports whether a session with this title is already stored.
//
// Titles are checked because the tmux session name is derived from the title:
// two sessions sharing one would fight over the same tmux session, and the
// second would appear to work while attaching to the first.
func titleTaken(title string) (bool, error) {
	state := config.LoadState()
	var stored []session.InstanceData
	if raw := state.GetInstances(); len(raw) > 0 {
		if err := json.Unmarshal(raw, &stored); err != nil {
			return false, couldNotLook("failed to read stored instances: %v", err)
		}
	}
	for _, d := range stored {
		if d.Title == title {
			return true, nil
		}
	}
	return false, nil
}

var newCmd = &cobra.Command{
	Use:   "new <title>",
	Short: "Create a session without opening the interface",
	Long: `Create a session without opening the interface.

Creates the git worktree, the branch and the tmux session, and records the
session in state so the interface lists it the next time it reads state.

  claude-squad new my-task
  claude-squad new my-task --repo ../other-repo
  claude-squad new my-task --program "aider"
  claude-squad new my-task --profile review

--profile names an entry in the profiles list in config; --program is a
literal command. They set the same thing, so passing both is refused. With
neither, default_program is used -- and note that it is matched against a
profile NAME first, so a profile's flags come along with it.

Talk to the session afterwards without the interface:

  tmux send-keys -t "$(claude-squad ls --json | jq -r '.[]|select(.title=="my-task").tmux_session')" 'hello' Enter

Safe to run while the interface is open: it re-reads state before saving, so a
session created here is not erased by an interface that started before it. The
interface lists it the next time it reads state.

Exit codes:

  0  created
  1  bad arguments
  2  refused -- understood and declined; a different title or path may work
  3  could not look -- state or tmux could not be read, so nothing was
     concluded and retrying the same call is unlikely to help`,
	Args: cobra.ExactArgs(1),
	RunE: func(command *cobra.Command, args []string) error {
		// ⛔ WITHOUT THIS THE COMMAND PANICS, and it panics LATE -- after the
		// worktree and branch already exist. session/tmux logs through
		// log.InfoLog, which is nil until Initialize runs.
		//
		// MEASURED 2026-08-26: `claude-squad new x --profile y` segfaulted at
		// session/tmux/tmux.go:185 on the history-limit warning, leaving a
		// worktree and branch behind with no session and no state entry.
		//
		// ⭐ kill.go carries the identical fix and the identical comment. It was
		// applied to ONE subcommand. `new` is the one nobody had run -- which is
		// why the CLI looked built and was not.
		log.Initialize(false)
		defer log.Close()

		title := args[0]

		taken, err := titleTaken(title)
		if err != nil {
			return err
		}
		if taken {
			return refused("a session named %q already exists", title)
		}

		repo := newRepo
		if repo == "" {
			repo, err = os.Getwd()
			if err != nil {
				return fmt.Errorf("failed to determine the working directory: %w", err)
			}
		}
		repo, err = filepath.Abs(repo)
		if err != nil {
			return fmt.Errorf("failed to resolve %q: %w", newRepo, err)
		}
		// Checked before anything is created: Start would otherwise fail partway
		// and leave the tmux session or the worktree behind.
		if !git.IsRepo(repo) {
			return refused("%s is not a git repository", repo)
		}

		program, err := resolveProgram(config.LoadConfig(), newProgram, newProfile)
		if err != nil {
			return err
		}

		// ⛔ REFRESH THE REMOTE-TRACKING REFS BEFORE THE WORKTREE IS CUT.
		//
		// resolveBaseRef() correctly prefers `origin/<branch>` over local HEAD, but
		// `origin/<branch>` is a CACHED ref in .git/refs/remotes/ — it is only as
		// current as the last fetch. Naming the remote ref is necessary and NOT
		// sufficient.
		//
		// MEASURED 2026-08-28 in ~/the-lab/NextActionGuide: local `develop` sat 30
		// commits behind origin, and a worktree cut from it started 30 commits in
		// the past. The TUI path already fetches (app/app.go, before the branch
		// picker). THIS path did not, and this is the path `ops/bin/dispatch` uses
		// on a systemd timer -- the unattended caller this file's own comment in
		// session/git/worktree_ops.go warns about: "A person running `cs new` might
		// notice the base looked odd. A timer never will."
		//
		// Best-effort by design: FetchBranches swallows its error, so an offline
		// machine still creates the session rather than refusing. The base is then
		// as current as the cache allows, which is the pre-existing behaviour.
		git.FetchBranches(repo)

		instance, err := session.NewInstance(session.InstanceOptions{
			Title:   title,
			Path:    repo,
			Program: program,
		})
		if err != nil {
			return err
		}

		// Start cleans up after itself on failure: if the tmux session cannot be
		// created it removes the worktree it just made.
		if err := instance.Start(true); err != nil {
			return err
		}

		if err := appendInstance(instance); err != nil {
			// The session is running but unrecorded, which is the state that makes
			// one invisible to the interface. Say so plainly rather than leaving it
			// to be discovered later.
			return fmt.Errorf(
				"session %q is running but could NOT be recorded in state: %w\n"+
					"its tmux session and worktree exist; it will not be listed until this is fixed",
				title, err)
		}

		fmt.Printf("%s\t%s\t%s\n", instance.Title, instance.Branch, repo)
		return nil
	},
}
