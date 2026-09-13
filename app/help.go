package app

import (
	"claude-squad/log"
	"claude-squad/session"
	"claude-squad/ui"
	"claude-squad/ui/overlay"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type helpText interface {
	// toContent returns the help UI content.
	toContent() string
	// mask returns the bit mask for this help text. These are used to track which help screens
	// have been seen in the config and app state.
	mask() uint32
	// actionKey returns an extra key (besides Enter) that proceeds a gating
	// overlay for this help type -- e.g. "c" for checkout, so the key that
	// opened the overlay also confirms it. Empty means Enter is the only
	// key that proceeds. Ignored for non-gating help types.
	actionKey() string
}

type helpTypeGeneral struct{}

type helpTypeInstanceStart struct {
	instance *session.Instance
}

type helpTypeInstanceAttach struct{}

type helpTypeInstanceCheckout struct{}

func helpStart(instance *session.Instance) helpText {
	return helpTypeInstanceStart{instance: instance}
}

func (h helpTypeGeneral) toContent() string {
	content := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("Claude Squad"),
		"",
		"A terminal UI that manages multiple Claude Code (and other local agents) in separate workspaces.",
		"",
		headerStyle.Render("Managing:"),
		keyStyle.Render("n")+descStyle.Render("         - Create a new session"),
		keyStyle.Render("N")+descStyle.Render("         - Create a new session with a prompt"),
		descStyle.Render("            (both ask which repository when repo_roots lists more than one;"),
		descStyle.Render("             N always asks which profile, n only with profile_on_new set)"),
		keyStyle.Render("D")+descStyle.Render("         - Kill (delete) the selected session"),
		keyStyle.Render("↑/j, ↓/k")+descStyle.Render("  - Navigate between sessions"),
		keyStyle.Render("J/K")+descStyle.Render("       - Reorder sessions"),
		keyStyle.Render("↵/o")+descStyle.Render("       - Attach to the selected session"),
		keyStyle.Render("ctrl-q")+descStyle.Render("    - Detach from session"),
		"",
		headerStyle.Render("Handoff:"),
		keyStyle.Render("p")+descStyle.Render("         - Commit and push branch to github"),
		keyStyle.Render("c")+descStyle.Render("         - Checkout: commit changes and pause session"),
		keyStyle.Render("r")+descStyle.Render("         - Resume a paused session"),
		"",
		headerStyle.Render("Other:"),
		keyStyle.Render("tab")+descStyle.Render("       - Switch between preview, diff, and terminal tabs"),
		keyStyle.Render("shift-↓/↑")+descStyle.Render(" - Scroll in preview/diff/terminal view"),
		keyStyle.Render("q")+descStyle.Render("         - Quit the application"),
		"",
		headerStyle.Render("Choosing a repository:"),
		descStyle.Render("Set ")+keyStyle.Render("repo_roots")+descStyle.Render(" in config.json to directories holding repositories."),
		descStyle.Render("Each is scanned one level deep, and ")+keyStyle.Render("n")+descStyle.Render("/")+keyStyle.Render("N")+descStyle.Render(" then offer the list. With none"),
		descStyle.Render("set, sessions are created in the working directory as before."),
		"",
		headerStyle.Render("Choosing a profile:"),
		descStyle.Render("A profile is a name and a command -- an agent with its flags. ")+keyStyle.Render("N")+descStyle.Render(" always offers"),
		descStyle.Render("the list, alongside the prompt box. Set ")+keyStyle.Render("profile_on_new")+descStyle.Render(" to offer it from ")+keyStyle.Render("n"),
		descStyle.Render("too, without a prompt. Unset, ")+keyStyle.Render("n")+descStyle.Render(" behaves exactly as before."),
		"",
		headerStyle.Render("Without this interface:"),
		keyStyle.Render("claude-squad ls")+descStyle.Render("            - List sessions (--json for scripts)"),
		keyStyle.Render("claude-squad new <title>")+descStyle.Render("   - Create one (--repo where, --profile which)"),
		keyStyle.Render("claude-squad kill <title>")+descStyle.Render("  - Remove one (--yes required; deletes the branch)"),
		descStyle.Render("Sessions are plain tmux sessions: ")+keyStyle.Render("tmux attach -t claudesquad_<title>"),
	)
	return content
}

func (h helpTypeInstanceStart) toContent() string {
	content := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("Instance Created"),
		"",
		descStyle.Render("New session created:"),
		descStyle.Render(fmt.Sprintf("• Git branch: %s (isolated worktree)",
			lipgloss.NewStyle().Bold(true).Render(h.instance.Branch))),
		descStyle.Render(fmt.Sprintf("• %s running in background tmux session",
			lipgloss.NewStyle().Bold(true).Render(h.instance.Program))),
		"",
		headerStyle.Render("Managing:"),
		keyStyle.Render("↵/o")+descStyle.Render("   - Attach to the session to interact with it directly"),
		keyStyle.Render("tab")+descStyle.Render("   - Switch preview panes to view session diff"),
		keyStyle.Render("D")+descStyle.Render("     - Kill (delete) the selected session"),
		"",
		headerStyle.Render("Handoff:"),
		keyStyle.Render("c")+descStyle.Render("     - Checkout this instance's branch"),
		keyStyle.Render("p")+descStyle.Render("     - Push branch to GitHub to create a PR"),
	)
	return content
}

func (h helpTypeInstanceAttach) toContent() string {
	content := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("Attaching to Instance"),
		"",
		descStyle.Render("To detach from a session, press ")+keyStyle.Render("ctrl-q"),
	)
	return content
}

func (h helpTypeInstanceCheckout) toContent() string {
	content := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("Checkout Instance"),
		"",
		"Changes will be committed locally. The branch name has been copied to your clipboard for you to checkout.",
		"",
		"Feel free to make changes to the branch and commit them. When resuming, the session will continue from where you left off.",
		"",
		headerStyle.Render("Commands:"),
		keyStyle.Render("c")+descStyle.Render(" - Checkout: commit changes locally and pause session"),
		keyStyle.Render("r")+descStyle.Render(" - Resume a paused session"),
	)
	return content
}
func (h helpTypeGeneral) mask() uint32 {
	return 1
}

func (h helpTypeInstanceStart) mask() uint32 {
	return 1 << 1
}
func (h helpTypeInstanceAttach) mask() uint32 {
	return 1 << 2
}
func (h helpTypeInstanceCheckout) mask() uint32 {
	return 1 << 3
}

func (h helpTypeGeneral) actionKey() string          { return "" }
func (h helpTypeInstanceStart) actionKey() string    { return "" }
func (h helpTypeInstanceAttach) actionKey() string   { return "" }
func (h helpTypeInstanceCheckout) actionKey() string { return "c" }

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Underline(true).Foreground(lipgloss.Color("#7D56F4"))
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#36CFC9"))
	keyStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFCC00"))
	descStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF"))
)

// showHelpScreen displays the help screen overlay if it hasn't been shown before
func (m *home) showHelpScreen(helpType helpText, onDismiss func()) (tea.Model, tea.Cmd) {
	// Get the flag for this help type
	var alwaysShow bool
	switch helpType.(type) {
	case helpTypeGeneral:
		alwaysShow = true
	}

	flag := helpType.mask()

	// Check if this help screen has been seen before
	// Only show if we're showing the general help screen or the corresponding flag is not set
	// in the seen bitmask.
	if alwaysShow || (m.appState.GetHelpScreensSeen()&flag) == 0 {
		// Mark this help screen as seen and save state
		if err := m.appState.SetHelpScreensSeen(m.appState.GetHelpScreensSeen() | flag); err != nil {
			log.WarningLog.Printf("Failed to save help screen state: %v", err)
		}

		content := helpType.toContent()

		m.textOverlay = overlay.NewTextOverlay(content)
		m.textOverlay.OnDismiss = onDismiss
		// A help screen that carries an action (onDismiss != nil) gates: only
		// Enter or the type's own action key proceeds, Esc/q cancel without
		// running the action, and any other key leaves the overlay open. A
		// screen with no callback (nil) has nothing to gate, so it keeps the
		// original dismiss-on-any-key behaviour.
		if onDismiss != nil {
			m.textOverlay.Gating = true
			m.textOverlay.ActionKey = helpType.actionKey()
		}
		m.state = stateHelp
		return m, nil
	}

	// Skip displaying the help screen
	if onDismiss != nil {
		onDismiss()
	}
	return m, nil
}

// handleHelpState handles key events when in help state
func (m *home) handleHelpState(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// shouldClose only means the overlay is done being shown -- it says
	// nothing about whether the underlying action ran. For a gating overlay,
	// HandleKeyPress decides that itself and calls OnDismiss (proceed) or
	// OnCancel (cancel) accordingly before returning true here.
	shouldClose := m.textOverlay.HandleKeyPress(msg)
	if shouldClose {
		m.state = stateDefault
		return m, tea.Sequence(
			tea.WindowSize(),
			func() tea.Msg {
				m.menu.SetState(ui.StateDefault)
				return nil
			},
		)
	}

	return m, nil
}
