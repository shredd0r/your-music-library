package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"your-music-library/internal/termimg"
)

// coverCols is the width of a cover in cells; it is square on screen.
const coverCols = 24

var coverRows = termimg.Rows(coverCols)

// coverMsg delivers a loaded cover image (nil data: there is none).
type coverMsg struct {
	key  string
	data []byte
	err  error
}

type coverState struct {
	loading  bool
	failed   bool
	none     bool
	rendered string
}

// covers loads, renders and caches cover images by key ("lib:<path>",
// "url:<url>"). Each cover is rendered once at a fixed size.
type covers struct {
	proto  termimg.Protocol
	state  map[string]*coverState
	nextID uint32
	sent   []uint32 // Kitty image ids, freed on exit
}

func newCovers(proto termimg.Protocol, firstID uint32) *covers {
	return &covers{proto: proto, state: map[string]*coverState{}, nextID: firstID}
}

// want returns a command loading key's image unless it is already known.
func (c *covers) want(key string, load func() ([]byte, error)) tea.Cmd {
	if key == "" {
		return nil
	}
	if _, ok := c.state[key]; ok {
		return nil
	}
	c.state[key] = &coverState{loading: true}
	return func() tea.Msg {
		data, err := load()
		return coverMsg{key: key, data: data, err: err}
	}
}

// update stores a loaded cover. With Kitty it returns the command that
// sends the image to the terminal.
func (c *covers) update(m coverMsg) tea.Cmd {
	st := &coverState{}
	c.state[m.key] = st
	if m.err != nil {
		st.failed = true
		return nil
	}
	if len(m.data) == 0 {
		st.none = true
		return nil
	}
	img, err := termimg.Decode(m.data)
	if err != nil {
		st.failed = true
		return nil
	}
	if c.proto != termimg.Kitty {
		st.rendered = termimg.HalfBlocks(img, coverCols, coverRows)
		return nil
	}
	id := c.nextID
	c.nextID = c.nextID%0xfffffe + 1 // ids live in the 24-bit foreground color
	seq, err := termimg.Transmit(img, id, coverCols, coverRows)
	if err != nil {
		st.failed = true
		return nil
	}
	c.sent = append(c.sent, id)
	st.rendered = termimg.Placeholder(id, coverCols, coverRows)
	return tea.Raw(seq)
}

// view returns the cover's lines, or a placeholder box with a note.
func (c *covers) view(key string) []string {
	if key == "" {
		return nil
	}
	st := c.state[key]
	note := ""
	switch {
	case st == nil:
	case st.loading:
		note = "loading cover…"
	case st.failed:
		note = "cover unavailable"
	case st.none:
		note = "no cover"
	default:
		return splitLines(st.rendered)
	}
	lines := make([]string, coverRows)
	if note != "" {
		pad := max(0, (coverCols-len(note))/2)
		lines[coverRows/2] = stDim.Render(strings.Repeat(" ", pad) + note)
	}
	return lines
}

// cleanup returns escape sequences that free the images sent to Kitty.
func (c *covers) cleanup() string {
	var b strings.Builder
	for _, id := range c.sent {
		b.WriteString(termimg.Delete(id))
	}
	return b.String()
}
