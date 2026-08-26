package main

import (
	"claude-squad/cmd"
	"claude-squad/config"
	"claude-squad/session"
	"claude-squad/session/tmux"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// instanceView is what `ls` reports. It is deliberately a separate shape from
// session.InstanceData: this is a published output format that scripts will
// parse, so it should not silently change when the stored representation does.
type instanceView struct {
	Title       string    `json:"title"`
	Repo        string    `json:"repo"`
	Branch      string    `json:"branch"`
	Status      string    `json:"status"`
	Worktree    string    `json:"worktree"`
	TmuxSession string    `json:"tmux_session"`
	TmuxAlive   bool      `json:"tmux_alive"`
	Program     string    `json:"program"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func statusName(s session.Status) string {
	switch s {
	case session.Running:
		return "running"
	case session.Ready:
		return "ready"
	case session.Loading:
		return "loading"
	case session.Paused:
		return "paused"
	default:
		return fmt.Sprintf("unknown(%d)", int(s))
	}
}

// loadInstanceViews reads stored instances WITHOUT starting anything.
//
// It deliberately does not use session.Storage.LoadInstances: that calls
// FromInstanceData, which calls Start(false) and restores a tmux session for
// every non-paused entry. Listing must not attach to anything, so this reads
// the stored JSON directly.
func loadInstanceViews() ([]instanceView, error) {
	state := config.LoadState()

	var stored []session.InstanceData
	if raw := state.GetInstances(); len(raw) > 0 {
		if err := json.Unmarshal(raw, &stored); err != nil {
			return nil, fmt.Errorf("failed to read stored instances: %w", err)
		}
	}

	// A missing tmux server means no live sessions, which is an answer. A real
	// failure to look is reported, so "not alive" never silently stands in for
	// "could not tell".
	live, err := tmux.LiveSessions(cmd.MakeExecutor())
	if err != nil {
		return nil, err
	}
	alive := make(map[string]struct{}, len(live))
	for _, name := range live {
		alive[name] = struct{}{}
	}

	views := make([]instanceView, 0, len(stored))
	for _, d := range stored {
		sessionName := tmux.SessionName(d.Title)
		_, isAlive := alive[sessionName]
		views = append(views, instanceView{
			Title:       d.Title,
			Repo:        d.Worktree.RepoPath,
			Branch:      d.Branch,
			Status:      statusName(d.Status),
			Worktree:    d.Worktree.WorktreePath,
			TmuxSession: sessionName,
			TmuxAlive:   isAlive,
			Program:     d.Program,
			CreatedAt:   d.CreatedAt,
			UpdatedAt:   d.UpdatedAt,
		})
	}
	return views, nil
}

func renderInstanceTable(w io.Writer, views []instanceView) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TITLE\tREPO\tBRANCH\tSTATUS\tTMUX")
	for _, v := range views {
		live := "gone"
		if v.TmuxAlive {
			live = "alive"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", v.Title, v.Repo, v.Branch, v.Status, live)
	}
	_ = tw.Flush()
}

var lsJSON bool

var lsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List sessions without opening the interface",
	Long: `List sessions without opening the interface.

Reads stored state and reports whether each session's tmux session still
exists. A session listed as gone still has its worktree and branch on disk and
can be resumed; one that is running but absent from this list is not tracked at
all and will not appear in the interface either.

Nothing is started, attached to or modified.`,
	RunE: func(command *cobra.Command, args []string) error {
		views, err := loadInstanceViews()
		if err != nil {
			return err
		}

		if lsJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			// Encode the empty slice as [] rather than null: a consumer should be
			// able to iterate the result without a nil check.
			return enc.Encode(views)
		}

		if len(views) == 0 {
			// No sessions is a real answer, not a failure. Say so on stderr so
			// that stdout stays empty and pipelines see nothing rather than prose.
			fmt.Fprintln(os.Stderr, "no sessions")
			return nil
		}
		renderInstanceTable(os.Stdout, views)
		return nil
	},
}
