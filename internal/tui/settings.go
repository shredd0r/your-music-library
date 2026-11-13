package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"your-music-library/internal/config"
	"your-music-library/internal/source"
	"your-music-library/internal/tags"
)

// settingsScreen shows the configuration and enables or disables plugins.
type settingsScreen struct {
	s      *shared
	cursor int
}

func newSettings(s *shared) *settingsScreen { return &settingsScreen{s: s} }

func (m *settingsScreen) name() string { return "Settings" }

func (m *settingsScreen) init() tea.Cmd { return nil }

func (m *settingsScreen) capturing() bool { return false }

func (m *settingsScreen) help() string {
	return "↑↓ select · enter enable/disable plugin · r check plugins"
}

func (m *settingsScreen) update(msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	plugins := m.s.sources.All()
	if c, ok := move(m.cursor, len(plugins), k.String(), 5); ok {
		m.cursor = c
		return nil
	}
	switch k.String() {
	case "r":
		return tea.Batch(status("checking plugins…"), checkPlugins(m.s))
	case "enter":
		if m.cursor < len(plugins) {
			return m.togglePlugin(plugins[m.cursor])
		}
	}
	return nil
}

func (m *settingsScreen) togglePlugin(p *source.Plugin) tea.Cmd {
	enable := !p.Enabled
	c, err := config.SetPluginEnabled(m.s.d.ConfigPath, p.Name, enable)
	if err != nil {
		return errStatus(err)
	}
	m.s.cfg, m.s.sources = c, source.FromConfig(c.Plugins)
	cmds := []tea.Cmd{checkPlugins(m.s)}
	if !enable {
		return tea.Batch(append(cmds, status("%s is disabled; its queued downloads wait", p.Name))...)
	}
	cmds = append(cmds, status("%s is enabled", p.Name))
	if m.s.counts.waiting > 0 && !m.s.dl.running {
		cmds = append(cmds, m.s.startDownloads(), loadQueue(m.s)) // downloads that waited for it
	}
	return tea.Batch(cmds...)
}

func (m *settingsScreen) view(w, h int) string {
	c := m.s.cfg
	plugins := m.s.sources.All()
	lines := []string{
		stBold.Render("Configuration") + stDim.Render("  (edit the file for other settings)"),
		field("Config file", m.s.d.ConfigPath),
		field("Library", c.LibraryPath),
		field("Path template", c.PathTemplate),
		field("Tags", fmt.Sprintf("fixed set, front cover ≤%d px", tags.CoverMaxPx)),
		field("Providers", strings.Join(c.Providers, " → ")),
		field("Min score", fmt.Sprintf("%.2f", c.Metadata.MinScore)),
		field("Downloads", fmt.Sprintf("%d at a time; wait %d min for offline plugins", c.Queue.Parallel, c.Plugins.OfflineWaitMinutes)),
		field("Covers", string(m.s.d.Covers)+stDim.Render("  (YMLIB_IMAGES=kitty|halfblock overrides)")),
		"",
		stBold.Render("Plugins") + stDim.Render("  (HTTP servers you run; add with `ymlib plugin add <name> <url>`)"),
	}
	if len(plugins) == 0 {
		lines = append(lines, stDim.Render("  none registered"))
	}
	room := max(1, h-len(lines))
	top := max(0, min(m.cursor-room/2, len(plugins)-room))
	for i := top; i < min(len(plugins), top+room); i++ {
		line := m.pluginRow(plugins[i])
		if i == m.cursor {
			line = stSel.Render(fit(ansi.Strip(line), w))
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (m *settingsScreen) pluginRow(p *source.Plugin) string {
	state := stOK.Render("enabled ")
	if !p.Enabled {
		state = stDim.Render("disabled")
	}
	note := m.s.pluginNote(p.Name)
	switch st := m.s.plugins[p.Name]; {
	case st.Online():
		note = stOK.Render(note) + stDim.Render(fmt.Sprintf(" (%s %s)", orDash(st.Health.Name), orDash(st.Health.Version)))
	case note == "checking…":
		note = stDim.Render(note)
	default:
		note = stWarn.Render(note)
	}
	return fmt.Sprintf("  %-14s %s  %s  %s", p.Name, state, note, stDim.Render(p.URL))
}
