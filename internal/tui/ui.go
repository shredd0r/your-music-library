package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Styles use the terminal's own 16 colors so the UI follows its theme.
var (
	stBold   = lipgloss.NewStyle().Bold(true)
	stDim    = lipgloss.NewStyle().Faint(true)
	stSel    = lipgloss.NewStyle().Reverse(true)
	stSelDim = lipgloss.NewStyle().Underline(true)
	stErr    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	stOK     = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	stWarn   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	stAccent = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	stTab    = lipgloss.NewStyle().Padding(0, 1)
	stTabOn  = lipgloss.NewStyle().Padding(0, 1).Reverse(true).Bold(true)
)

// fit truncates s (which may contain escape sequences) to w cells and pads
// it with spaces to exactly w cells.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = ansi.Truncate(s, w, "…")
	if n := ansi.StringWidth(s); n < w {
		s += strings.Repeat(" ", w-n)
	}
	return s
}

// block returns exactly h lines of exactly w cells.
func block(lines []string, w, h int) string {
	out := make([]string, h)
	for i := range out {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}
		out[i] = fit(line, w)
	}
	return strings.Join(out, "\n")
}

// splitLines splits a rendered block into its lines.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// columns joins blocks side by side with a one-cell gap; each block is
// fitted to its width so the columns line up.
func columns(h int, parts ...column) string {
	rendered := make([]string, 0, len(parts)*2)
	for i, p := range parts {
		if i > 0 {
			rendered = append(rendered, block(nil, 1, h))
		}
		rendered = append(rendered, block(p.lines, p.width, h))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, rendered...)
}

type column struct {
	lines []string
	width int
}

// listLines renders rows [0,n) into h lines, scrolled so the cursor is
// visible, with the cursor row highlighted.
func listLines(n, cursor, w, h int, focused bool, row func(i int) string) []string {
	if h <= 0 {
		return nil
	}
	start := max(0, min(cursor-h/2, n-h))
	var lines []string
	for i := start; i < min(n, start+h); i++ {
		line := row(i)
		if i == cursor {
			line = fit(ansi.Strip(line), w)
			if focused {
				line = stSel.Render(line)
			} else {
				line = stSelDim.Render(line)
			}
		}
		lines = append(lines, line)
	}
	return lines
}

// move returns cursor moved by delta for the key, clamped to [0,n).
func move(cursor, n int, key string, page int) (int, bool) {
	switch key {
	case "up", "k":
		cursor--
	case "down", "j":
		cursor++
	case "pgup":
		cursor -= max(1, page)
	case "pgdown":
		cursor += max(1, page)
	case "home", "g":
		cursor = 0
	case "end", "G":
		cursor = n - 1
	default:
		return cursor, false
	}
	return max(0, min(cursor, n-1)), true
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// field renders "label  value" for detail panes.
func field(label, value string) string {
	return stDim.Render(fit(label, 13)) + value
}

// wrapPath breaks a long path after '/' (or anywhere, if needed) into
// dimmed lines of at most w cells.
func wrapPath(p string, w int) []string {
	return splitLines(stDim.Render(ansi.Wrap(p, max(1, w), "/")))
}
