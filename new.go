package main

import (
	"claude-squad/config"
	"claude-squad/session"
	"claude-squad/session/git"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var (
	newRepo    string
	newProgram string
)

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

		program := newProgram
		if program == "" {
			program = config.LoadConfig().DefaultProgram
		}

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
