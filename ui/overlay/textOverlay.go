package overlay

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// TextOverlay represents a text screen overlay
type TextOverlay struct {
	// Whether the overlay has been dismissed
	Dismissed bool
	// Callback function to be called when the overlay is dismissed by proceeding
	// (Enter, or ActionKey when set). For a non-Gating overlay this also runs on
	// any other key, since there is nothing to distinguish read-from-act.
	OnDismiss func()
	// Callback function to be called when a Gating overlay is cancelled (Esc/q).
	// Never called for a non-Gating overlay.
	OnCancel func()
	// Gating, when true, requires Enter (or ActionKey) to proceed. Esc and q
	// cancel without running OnDismiss. Any other key is ignored and the
	// overlay stays open. When false (the default), any key dismisses and
	// runs OnDismiss, matching the overlay's original behaviour.
	Gating bool
	// ActionKey, in Gating mode, is an extra key (besides Enter) that also
	// proceeds -- e.g. "c" for a checkout overlay reachable by pressing "c"
	// again. Empty means Enter is the only key that proceeds.
	ActionKey string
	// Content to display in the overlay
	content string

	width int
}

// NewTextOverlay creates a new text screen overlay with the given title and content
func NewTextOverlay(content string) *TextOverlay {
	return &TextOverlay{
		Dismissed: false,
		content:   content,
	}
}

// HandleKeyPress processes a key press and updates the state.
// Returns true if the overlay should be closed.
//
// Non-gating overlays close on any key and always run OnDismiss, unchanged
// from the original behaviour. A gating overlay separates "read it" from
// "act on it": only Enter or ActionKey proceeds (runs OnDismiss); Esc/q
// cancel (runs OnCancel, never OnDismiss); anything else is ignored and the
// overlay stays open.
func (t *TextOverlay) HandleKeyPress(msg tea.KeyMsg) bool {
	if !t.Gating {
		t.Dismissed = true
		if t.OnDismiss != nil {
			t.OnDismiss()
		}
		return true
	}

	switch msg.Type {
	case tea.KeyEnter:
		t.Dismissed = true
		if t.OnDismiss != nil {
			t.OnDismiss()
		}
		return true
	case tea.KeyEsc:
		t.Dismissed = true
		if t.OnCancel != nil {
			t.OnCancel()
		}
		return true
	}

	if t.ActionKey != "" && msg.String() == t.ActionKey {
		t.Dismissed = true
		if t.OnDismiss != nil {
			t.OnDismiss()
		}
		return true
	}

	if msg.String() == "q" {
		t.Dismissed = true
		if t.OnCancel != nil {
			t.OnCancel()
		}
		return true
	}

	// Any other key: ignored, overlay stays open.
	return false
}

// Render renders the text overlay
func (t *TextOverlay) Render(opts ...WhitespaceOption) string {
	content := t.content
	if t.Gating {
		footer := "enter to continue · esc to cancel"
		if t.ActionKey != "" {
			footer = fmt.Sprintf("enter/%s to continue · esc to cancel", t.ActionKey)
		}
		content = lipgloss.JoinVertical(lipgloss.Left,
			content,
			"",
			lipgloss.NewStyle().Faint(true).Render(footer),
		)
	}

	// Create styles
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("62")).
		Padding(1, 2).
		Width(t.width)

	// Apply the border style and return
	return style.Render(content)
}

func (t *TextOverlay) SetWidth(width int) {
	t.width = width
}
