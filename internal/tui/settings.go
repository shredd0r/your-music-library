package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"your-music-library/internal/config"
	"your-music-library/internal/secrets"
	"your-music-library/internal/source"
	"your-music-library/internal/tags"
)

// settingsScreen shows the configuration and adds, removes, enables or
// disables plugins.
type settingsScreen struct {
	s      *shared
	cursor int
	// add is the "add plugin" form (name, URL); nil when closed.
	add      []textinput.Model
	addFocus int
	// removing names the plugin waiting for a y/n confirmation.
	removing string
}

func newSettings(s *shared) *settingsScreen { return &settingsScreen{s: s} }

func (m *settingsScreen) name() string { return "Settings" }

func (m *settingsScreen) init() tea.Cmd { return nil }

func (m *settingsScreen) capturing() bool { return m.add != nil || m.removing != "" }

func (m *settingsScreen) help() string {
	switch {
	case m.add != nil:
		return "tab next field · enter add plugin · esc cancel"
	case m.removing != "":
		return "y remove " + m.removing + " · n keep it"
	}
	return "↑↓ select · enter enable/disable · a add plugin · x remove plugin · r check plugins"
}

func (m *settingsScreen) update(msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	if m.add != nil {
		return m.addKey(k)
	}
	if m.removing != "" {
		return m.removeKey(k.String())
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
	case "a":
		name, url := newInput("Name: "), newInput("URL:  ")
		name.Placeholder, url.Placeholder = "myplugin", "http://127.0.0.1:8765"
		m.add, m.addFocus = []textinput.Model{name, url}, 0
		return m.add[0].Focus()
	case "x":
		if m.cursor < len(plugins) {
			m.removing = plugins[m.cursor].Name
		}
	}
	return nil
}

func (m *settingsScreen) addKey(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "esc":
		m.add = nil
		return status("no plugin added")
	case "tab", "down", "shift+tab", "up":
		m.add[m.addFocus].Blur()
		m.addFocus = 1 - m.addFocus
		return m.add[m.addFocus].Focus()
	case "enter":
		name, url := strings.TrimSpace(m.add[0].Value()), strings.TrimSpace(m.add[1].Value())
		c, err := config.AddPlugin(m.s.d.ConfigPath, name, url)
		if err != nil {
			return errStatus(err)
		}
		m.add = nil
		m.s.cfg, m.s.sources = c, source.FromConfig(c.Plugins)
		return tea.Batch(status("added %s (checking whether it is online…)", name), checkPlugins(m.s))
	}
	var cmd tea.Cmd
	m.add[m.addFocus], cmd = m.add[m.addFocus].Update(k)
	return cmd
}

func (m *settingsScreen) removeKey(key string) tea.Cmd {
	name := m.removing
	switch key {
	case "y":
		m.removing = ""
		c, err := config.RemovePlugin(m.s.d.ConfigPath, name)
		if err != nil {
			return errStatus(err)
		}
		m.s.cfg, m.s.sources = c, source.FromConfig(c.Plugins)
		m.cursor = max(0, min(m.cursor, len(m.s.sources.All())-1))
		return status("removed %s", name)
	case "n", "esc":
		m.removing = ""
		return status("kept %s", name)
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
		field("Discogs token", m.tokenStatus()),
		"",
		stBold.Render("Plugins") + stDim.Render("  (HTTP servers you run)"),
	}
	switch {
	case m.add != nil:
		lines = append(lines, "  Add a plugin: the name is yours (used in references); the URL is the server's address.",
			"  "+m.add[0].View(), "  "+m.add[1].View(), "")
	case m.removing != "":
		lines = append(lines, stWarn.Render("  Remove "+m.removing+"? Its queued downloads will fail. y/n"), "")
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

func (m *settingsScreen) tokenStatus() string {
	switch m.s.d.DiscogsToken {
	case secrets.SourceKeychain:
		return stOK.Render("set") + stDim.Render(" (keychain)")
	case secrets.SourceEnv:
		return stOK.Render("set") + stDim.Render(" (YMLIB_DISCOGS_TOKEN)")
	}
	return stWarn.Render("not set") + stDim.Render("  (Discogs is skipped; see the README to add one)")
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
