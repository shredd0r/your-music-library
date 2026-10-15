package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"your-music-library/internal/config"
)

// settingsScreen shows the configuration and switches the active preset.
type settingsScreen struct {
	s      *shared
	cursor int
}

func newSettings(s *shared) *settingsScreen {
	m := &settingsScreen{s: s}
	for i, n := range s.cfg.PresetNames() {
		if n == s.cfg.ActivePreset {
			m.cursor = i
		}
	}
	return m
}

func (m *settingsScreen) name() string { return "Settings" }

func (m *settingsScreen) init() tea.Cmd { return nil }

func (m *settingsScreen) capturing() bool { return false }

func (m *settingsScreen) help() string { return "↑↓ preset · enter make active" }

func (m *settingsScreen) update(msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	names := m.s.cfg.PresetNames()
	if c, ok := move(m.cursor, len(names), k.String(), 5); ok {
		m.cursor = c
		return nil
	}
	if k.String() != "enter" || m.cursor >= len(names) {
		return nil
	}
	name := names[m.cursor]
	if name == m.s.cfg.ActivePreset {
		return status("%q is already the active preset", name)
	}
	c, err := config.SetActivePreset(m.s.d.ConfigPath, name)
	if err != nil {
		return errStatus(err)
	}
	m.s.cfg = c
	msg2 := fmt.Sprintf("active preset is now %q (saved to %s)", name, m.s.d.ConfigPath)
	if m.s.dl.running {
		msg2 += "; running downloads keep the previous preset"
	}
	return status("%s", msg2)
}

func (m *settingsScreen) view(w, h int) string {
	c := m.s.cfg
	plugins := []string{}
	for _, p := range m.s.d.Sources.All() {
		plugins = append(plugins, p.Name)
	}
	lines := []string{
		stBold.Render("Configuration") + stDim.Render("  (edit "+m.s.d.ConfigPath+" for other settings)"),
		field("Library", c.LibraryPath),
		field("Path template", c.PathTemplate),
		field("Singles", string(c.Singles)),
		field("Providers", strings.Join(c.Providers, " → ")),
		field("Min score", fmt.Sprintf("%.2f", c.Metadata.MinScore)),
		field("Downloads", fmt.Sprintf("%d at a time", c.Queue.Parallel)),
		field("Sources", fmt.Sprintf("%s: %s", m.s.d.SourcesDir, orDash(strings.Join(plugins, ", ")))),
		field("Covers", string(m.s.d.Covers)+stDim.Render("  (YMLIB_IMAGES=kitty|halfblock overrides)")),
		"",
		stBold.Render("Presets") + stDim.Render("  (* = active, used by identify, import and downloads)"),
	}
	names := c.PresetNames()
	listH := min(len(names), max(1, h-len(lines)-3))
	lines = append(lines, listLines(len(names), m.cursor, w, listH, true, func(i int) string {
		p, _ := c.Preset(names[i])
		mark := " "
		if names[i] == c.ActivePreset {
			mark = stOK.Render("*")
		}
		covers := "original size"
		if p.CoverMaxPx > 0 {
			covers = fmt.Sprintf("≤%d px", p.CoverMaxPx)
		}
		kind := "user"
		if p.Builtin {
			kind = "built-in"
		}
		return fmt.Sprintf("%s %-14s %s", mark, p.Name, stDim.Render(fmt.Sprintf("%s · %d fields · covers %s", kind, len(p.Fields), covers)))
	})...)
	if m.cursor < len(names) {
		p, _ := c.Preset(names[m.cursor])
		fields := make([]string, len(p.Fields))
		for i, f := range p.Fields {
			fields[i] = string(f)
		}
		lines = append(lines, "", field("Fields", strings.Join(fields, ", ")))
	}
	return strings.Join(lines, "\n")
}
