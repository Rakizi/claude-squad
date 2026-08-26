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
	Title       string `json:"title"`
	Repo        string `json:"repo"`
	Branch      string `json:"branch"`
	Status      string `json:"status"`
	Worktree    string `json:"worktree"`
	TmuxSession string `json:"tmux_session"`
	TmuxAlive   bool   `json:"tmux_alive"`
	//: Tmux is the SAME fact as TmuxAlive plus the one distinction a bool
	//: cannot carry: a PAUSED session has no tmux session BY DESIGN, and
	//: reporting that as "gone" reads as damage. Added alongside rather than
	//: replacing tmux_alive, because the JSON shape is a contract.
	//:   alive  the session exists
	//:   gone   it should exist and does not -- a problem
	//:   n/a    the instance is paused; absence is expected
	Tmux string `json:"tmux"`
	//: WorktreeState is what the DISK says about the path Worktree CLAIMS.
	//: MEASURED 2026-08-26: a paused session left state reading
	//: "ready · alive · <path>" while the path was gone and the session was
	//: not running. ls verified the tmux claim and took every other field on
	//: trust, so it repeated the store verbatim -- a mirror, not a report.
	//:   present  the path is there
	//:   missing  state names a path that is NOT there -- stale state, or the
	//:            tree was removed behind its back
	//:   n/a      paused; the worktree is torn down on purpose
	//:   unknown  unreadable for some OTHER reason. Per-ROW, because one bad
	//:            path must not blind the whole listing the way an
	//:            unreachable tmux rightly does.
	WorktreeState string    `json:"worktree_state"`
	Program       string    `json:"program"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
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

// worktreeState asks the DISK what state's worktree claim is worth.
//
// ⛔ ls already refuses to trust tmux; it took every other field on trust. That
// asymmetry is how a paused session printed a worktree path that no longer
// existed, with nothing marking it.
func worktreeState(status session.Status, path string) string {
	if path == "" {
		return "n/a"
	}
	if _, err := os.Stat(path); err == nil {
		return "present"
	} else if !os.IsNotExist(err) {
		// Not absent -- UNREADABLE. Reporting "missing" here would turn a
		// permission problem into a removal.
		return "unknown"
	}
	if status == session.Paused {
		return "n/a"
	}
	return "missing"
}

// tmuxState reports what the ABSENCE of a tmux session means for this instance.
//
// ⛔ "gone" and "not expected to be there" are different facts and a bool cannot
// hold both. A PAUSED session is one that `c` checked out: it committed, tore
// down the worktree and the tmux session, and KEPT the branch. Its tmux session
// is supposed to be absent. Printing "gone" for it invites someone to treat a
// working feature as damage.
//
// ⚠ The third state a reader might expect -- "could not tell" -- is handled
// EARLIER and harder: if tmux cannot be listed at all, loadInstanceViews returns
// couldNotLook and nothing is printed. So by the time this runs, absence is a
// measured fact rather than a failed measurement.
func tmuxState(status session.Status, alive bool) string {
	if alive {
		return "alive"
	}
	if status == session.Paused {
		return "n/a"
	}
	return "gone"
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
			return nil, couldNotLook("failed to read stored instances: %v", err)
		}
	}

	// A missing tmux server means no live sessions, which is an answer. A real
	// failure to look is reported, so "not alive" never silently stands in for
	// "could not tell".
	live, err := tmux.LiveSessions(cmd.MakeExecutor())
	if err != nil {
		// Whether sessions are alive is UNKNOWN, not false. Reporting them as
		// gone would be a lie a script would act on.
		return nil, couldNotLook("failed to list tmux sessions: %v", err)
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
			Title:         d.Title,
			Repo:          d.Worktree.RepoPath,
			Branch:        d.Branch,
			Status:        statusName(d.Status),
			Worktree:      d.Worktree.WorktreePath,
			TmuxSession:   sessionName,
			TmuxAlive:     isAlive,
			Tmux:          tmuxState(d.Status, isAlive),
			WorktreeState: worktreeState(d.Status, d.Worktree.WorktreePath),
			Program:       d.Program,
			CreatedAt:     d.CreatedAt,
			UpdatedAt:     d.UpdatedAt,
		})
	}
	return views, nil
}

func renderInstanceTable(w io.Writer, views []instanceView) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TITLE\tREPO\tBRANCH\tSTATUS\tTMUX\tWORKTREE")
	for _, v := range views {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", v.Title, v.Repo, v.Branch, v.Status, v.Tmux, v.WorktreeState)
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

Nothing is started, attached to or modified.

Exit codes:

  0  listed -- including "no sessions", which is an answer, not a failure
  3  could not look -- state or tmux could not be read. Sessions are NOT
     reported as gone in that case, because unknown is not the same as absent.`,
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
