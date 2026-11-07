package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"your-music-library/internal/config"
	"your-music-library/internal/source"
)

// settingsScreen shows the configuration, enables or disables plugins and
// switches the active preset. One list holds the plugins, then the presets.
type settingsScreen struct {
	s      *shared
	cursor int
}

func newSettings(s *shared) *settingsScreen {
	m := &settingsScreen{s: s}
	for i, n := range s.cfg.PresetNames() {
		if n == s.cfg.ActivePreset {
			m.cursor = len(s.sources.All()) + i
		}
	}
	return m
}

func (m *settingsScreen) name() string { return "Settings" }

func (m *settingsScreen) init() tea.Cmd { return nil }

func (m *settingsScreen) capturing() bool { return false }

func (m *settingsScreen) help() string {
	if m.cursor < len(m.s.sources.All()) {
		return "↑↓ select · enter enable/disable plugin · r check plugins"
	}
	return "↑↓ select · enter make preset active · r check plugins"
}

func (m *settingsScreen) update(msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	plugins, names := m.s.sources.All(), m.s.cfg.PresetNames()
	if c, ok := move(m.cursor, len(plugins)+len(names), k.String(), 5); ok {
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
		if i := m.cursor - len(plugins); i < len(names) {
			return m.activate(names[i])
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

func (m *settingsScreen) activate(name string) tea.Cmd {
	if name == m.s.cfg.ActivePreset {
		return status("%q is already the active preset", name)
	}
	c, err := config.SetActivePreset(m.s.d.ConfigPath, name)
	if err != nil {
		return errStatus(err)
	}
	m.s.cfg = c
	msg := fmt.Sprintf("active preset is now %q (saved to %s)", name, m.s.d.ConfigPath)
	if m.s.dl.running {
		msg += "; running downloads keep the previous preset"
	}
	return status("%s", msg)
}

func (m *settingsScreen) view(w, h int) string {
	c := m.s.cfg
	plugins, names := m.s.sources.All(), c.PresetNames()
	lines := []string{
		stBold.Render("Configuration") + stDim.Render("  (edit "+m.s.d.ConfigPath+" for other settings)"),
		field("Library", c.LibraryPath),
		field("Path template", c.PathTemplate),
		field("Singles", string(c.Singles)),
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
	// rows: plugins, a gap, the presets heading, presets
	rows := len(plugins) + len(names)
	room := max(1, h-len(lines)-6)
	top := max(0, min(m.cursor-room/2, rows-room))
	row := func(i int) string {
		if i < len(plugins) {
			return m.pluginRow(plugins[i])
		}
		return m.presetRow(names[i-len(plugins)])
	}
	for i := top; i < min(rows, top+room); i++ {
		if i == len(plugins) {
			lines = append(lines, "", stBold.Render("Presets")+stDim.Render("  (* = active, used by identify, import and downloads)"))
		}
		line := row(i)
		if i == m.cursor {
			line = stSel.Render(fit(ansi.Strip(line), w))
		}
		lines = append(lines, line)
	}
	if i := m.cursor - len(plugins); i >= 0 && i < len(names) {
		p, _ := c.Preset(names[i])
		fields := make([]string, len(p.Fields))
		for i, f := range p.Fields {
			fields[i] = string(f)
		}
		lines = append(lines, "", field("Fields", strings.Join(fields, ", ")))
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

func (m *settingsScreen) presetRow(name string) string {
	c := m.s.cfg
	p, _ := c.Preset(name)
	mark := " "
	if name == c.ActivePreset {
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
}
