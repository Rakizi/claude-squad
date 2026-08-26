package overlay

import (
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// RepoPicker selects which repository a new session is created in.
//
// It lists vertically rather than horizontally like ProfilePicker: repository
// names are long, and several roots can be configured, so a single line would
// wrap and stop being scannable.
type RepoPicker struct {
	repos   []string
	cursor  int
	focused bool
	width   int
}

// NewRepoPicker creates a picker over the given repository paths. The first is
// selected, and callers put the working directory there so that confirming
// immediately reproduces the previous behaviour.
func NewRepoPicker(repos []string) *RepoPicker {
	return &RepoPicker{repos: repos}
}

// Focus gives the picker focus.
func (rp *RepoPicker) Focus() { rp.focused = true }

// Blur removes focus from the picker.
func (rp *RepoPicker) Blur() { rp.focused = false }

// SetWidth sets the rendering width.
func (rp *RepoPicker) SetWidth(w int) { rp.width = w }

// HandleKeyPress processes a key event. Returns true if it was consumed.
func (rp *RepoPicker) HandleKeyPress(msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyUp:
		if rp.cursor > 0 {
			rp.cursor--
		}
		return true
	case tea.KeyDown:
		if rp.cursor < len(rp.repos)-1 {
			rp.cursor++
		}
		return true
	}
	switch msg.String() {
	case "k":
		if rp.cursor > 0 {
			rp.cursor--
		}
		return true
	case "j":
		if rp.cursor < len(rp.repos)-1 {
			rp.cursor++
		}
		return true
	}
	return false
}

// GetSelectedRepo returns the selected repository path, or "" when there is
// nothing to select. Callers treat "" as "use the working directory", which is
// what the hardcoded "." used to mean.
func (rp *RepoPicker) GetSelectedRepo() string {
	if rp.cursor < 0 || rp.cursor >= len(rp.repos) {
		if len(rp.repos) == 0 {
			return ""
		}
		return rp.repos[0]
	}
	return rp.repos[rp.cursor]
}

// HasMultiple reports whether there is an actual choice to make. With one
// repository the picker is skipped entirely, so an unconfigured install never
// sees a prompt that has only one answer.
func (rp *RepoPicker) HasMultiple() bool { return len(rp.repos) > 1 }

// rpPadX is the horizontal padding, named once so Render and boxWidth cannot
// disagree about it.
const rpPadX = 2

var (
	rpLabelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("62")).
			Bold(true)

	rpSelectedStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("62")).
			Foreground(lipgloss.Color("0"))

	rpDimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))
)

// Render draws the picker inside a bordered box.
//
// ProfilePicker returns bare text because it is embedded inside another
// overlay that supplies the frame. This one is placed directly over the main
// view, so without its own opaque box the view behind shows through it.
func (rp *RepoPicker) Render() string {
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("62")).
		Padding(1, rpPadX)
	if w := rp.boxWidth(); w > 0 {
		style = style.Width(w)
	}
	return style.Render(rp.content())
}

// boxWidth sizes the box to its CONTENT, capped by whatever the caller set as
// the available width.
//
// Setting it to a fraction of the terminal instead wraps the longest entry onto
// a second line, which breaks the one-repository-per-line reading the list
// depends on. Content decides the width; the terminal only decides the ceiling.
func (rp *RepoPicker) boxWidth() int {
	longest := len("enter to create here · esc to cancel")
	for _, repo := range rp.repos {
		if n := rp.nameColumn() + 2 + len(filepath.Dir(repo)); n > longest {
			longest = n
		}
	}
	// The space either side of the name, plus the style's own horizontal padding:
	// lipgloss Width() counts padding INSIDE the value, so leaving it out here
	// hands the content four fewer columns than it needs and wraps every line.
	longest += 2 + (rpPadX * 2)
	if rp.width > 0 && longest > rp.width {
		return rp.width
	}
	return longest
}

// nameColumn is the width the repository names are padded to, so the paths line
// up in a column instead of stepping raggedly across the box.
func (rp *RepoPicker) nameColumn() int {
	w := 0
	for _, repo := range rp.repos {
		if n := len(filepath.Base(repo)); n > w {
			w = n
		}
	}
	return w
}

func (rp *RepoPicker) content() string {
	var s strings.Builder
	s.WriteString(rpLabelStyle.Render("Repository"))
	if rp.HasMultiple() && rp.focused {
		s.WriteString(rpDimStyle.Render("  ↑/↓ to change"))
	}
	s.WriteString("\n\n")

	col := rp.nameColumn()
	for i, repo := range rp.repos {
		name := filepath.Base(repo)
		parent := filepath.Dir(repo)

		line := " " + name + strings.Repeat(" ", col-len(name)) + " "
		switch {
		case i == rp.cursor && rp.focused:
			s.WriteString(rpSelectedStyle.Render(line))
		case i == rp.cursor:
			s.WriteString(line)
		default:
			s.WriteString(rpDimStyle.Render(line))
		}
		s.WriteString(rpDimStyle.Render("  " + parent))
		if i < len(rp.repos)-1 {
			s.WriteString("\n")
		}
	}

	s.WriteString("\n\n")
	s.WriteString(rpDimStyle.Render("enter to create here · esc to cancel"))

	return s.String()
}
