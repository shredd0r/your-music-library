package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"your-music-library/internal/library"
	"your-music-library/internal/source"
)

const searchLimit = 30

type searchResult struct {
	source  string
	item    source.Item
	dup     library.DupStatus
	dupPath string
}

func (r searchResult) ref() source.Ref { return source.Ref{Source: r.source, ID: r.item.ID} }

type searchMsg struct {
	seq     int
	results []searchResult
	notes   []string
}

// searchScreen searches source plugins and queues downloads.
type searchScreen struct {
	s         *shared
	input     textinput.Model
	kind      source.Kind
	results   []searchResult
	cursor    int
	typing    bool
	busy      bool
	seq       int
	notes     []string
	queued    map[string]bool
	lastQuery string
	lastKind  source.Kind
}

func newInput(prompt string) textinput.Model {
	in := textinput.New()
	in.Prompt = prompt
	st := in.Styles()
	st.Cursor.Blink = false
	in.SetStyles(st)
	in.SetWidth(60) // without a width only one character of the placeholder shows
	return in
}

func newSearch(s *shared) *searchScreen {
	m := &searchScreen{s: s, input: newInput("Search tracks: "), kind: source.KindTrack, queued: map[string]bool{}}
	m.input.Placeholder = "press / to search by artist, title or album"
	return m
}

func (m *searchScreen) name() string { return "Search" }

func (m *searchScreen) init() tea.Cmd { return nil }

func (m *searchScreen) capturing() bool { return m.typing }

func (m *searchScreen) help() string {
	if m.typing {
		return "enter search · ctrl+t tracks/albums · esc leave the search box"
	}
	if len(m.results) == 0 {
		return "/ or enter to search · t tracks/albums"
	}
	return "↑↓ select · enter/d download · / new search · t tracks/albums"
}

func (m *searchScreen) setKind(k source.Kind) {
	m.kind = k
	if k == source.KindAlbum {
		m.input.Prompt = "Search albums: "
	} else {
		m.input.Prompt = "Search tracks: "
	}
}

func (m *searchScreen) toggleKind() tea.Cmd {
	if m.kind == source.KindTrack {
		m.setKind(source.KindAlbum)
	} else {
		m.setKind(source.KindTrack)
	}
	if m.lastQuery != "" {
		return m.search()
	}
	return nil
}

func (m *searchScreen) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case searchMsg:
		if msg.seq != m.seq {
			return nil // an older search
		}
		m.busy, m.results, m.notes, m.cursor = false, msg.results, msg.notes, 0
		if len(m.results) > 0 {
			m.typing = false
			m.input.Blur()
		}
		return m.wantCover()
	case tea.KeyPressMsg:
		if m.typing {
			return m.typingKey(msg)
		}
		return m.resultsKey(msg)
	}
	return nil
}

func (m *searchScreen) typingKey(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "enter":
		return m.search()
	case "ctrl+t":
		return m.toggleKind()
	case "esc":
		m.typing = false
		m.input.Blur()
		return nil
	case "down":
		if len(m.results) > 0 {
			m.typing = false
			m.input.Blur()
		}
		return nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	return cmd
}

func (m *searchScreen) resultsKey(k tea.KeyPressMsg) tea.Cmd {
	key := k.String()
	if c, ok := move(m.cursor, len(m.results), key, 10); ok {
		if c == m.cursor && key == "up" {
			m.typing = true
			return m.input.Focus()
		}
		m.cursor = c
		return m.wantCover()
	}
	switch key {
	case "enter", "d":
		if len(m.results) == 0 {
			m.typing = true
			return m.input.Focus()
		}
		return m.download()
	case "/", "i":
		m.typing = true
		m.input.SetValue("")
		return m.input.Focus()
	case "t", "ctrl+t":
		return m.toggleKind()
	}
	return nil
}

func (m *searchScreen) search() tea.Cmd {
	q := strings.TrimSpace(m.input.Value())
	if q == "" {
		return nil
	}
	if len(m.s.d.Sources.All()) == 0 {
		return errStatus(fmt.Errorf("no source plugins in %s", m.s.d.SourcesDir))
	}
	m.seq++
	m.busy, m.lastQuery, m.lastKind = true, q, m.kind
	seq, kind, s := m.seq, m.kind, m.s
	return func() tea.Msg {
		out := searchMsg{seq: seq}
		var dups *library.DupIndex
		if kind == source.KindTrack {
			var err error
			if dups, err = s.d.Lib.DupIndex(s.ctx); err != nil {
				out.notes = append(out.notes, "duplicate check: "+err.Error())
			}
		}
		for _, p := range s.d.Sources.All() {
			items, err := p.Search(s.ctx, q, kind, searchLimit)
			if err != nil {
				out.notes = append(out.notes, err.Error())
				continue
			}
			for _, it := range items {
				r := searchResult{source: p.Name, item: it}
				if dups != nil {
					r.dup, r.dupPath = dups.Check(library.DupQuery{Source: p.Name, SourceTrackID: it.ID, ISRC: it.ISRC,
						Artist: it.Artist, AlbumArtist: it.AlbumArtist, Album: it.Album, Title: it.Title})
				}
				out.results = append(out.results, r)
			}
		}
		return out
	}
}

func (m *searchScreen) wantCover() tea.Cmd {
	if m.cursor >= len(m.results) || m.s.d.Images == nil {
		return nil
	}
	url := m.results[m.cursor].item.CoverURL
	if url == "" {
		return nil
	}
	s := m.s
	return s.covers.want("url:"+url, func() ([]byte, error) {
		data, _, err := s.d.Images.Fetch(s.ctx, url)
		return data, err
	})
}

func (m *searchScreen) download() tea.Cmd {
	if m.cursor >= len(m.results) {
		return nil
	}
	r := m.results[m.cursor]
	if m.lastKind == source.KindTrack && r.dup == library.InLibrary {
		return status("✓ already in the library: %s", r.dupPath)
	}
	j, created, err := m.s.d.Queue.Enqueue(m.s.ctx, r.source, m.lastKind, r.item, 0)
	if err != nil {
		return errStatus(err)
	}
	m.queued[r.ref().String()] = true
	msg := status("queued %s — %s (job %d)", r.item.Artist, r.item.Title, j.ID)
	if !created {
		msg = status("%s — %s is already in the queue (job %d, %s)", r.item.Artist, r.item.Title, j.ID, j.Status)
	}
	return tea.Batch(msg, loadQueue(m.s), m.s.startDownloads())
}

func (m *searchScreen) view(w, h int) string {
	var lines []string
	lines = append(lines, m.input.View())
	switch {
	case len(m.s.d.Sources.All()) == 0:
		lines = append(lines, stWarn.Render("No source plugins in "+m.s.d.SourcesDir+
			". A plugin is an executable speaking the protocol in internal/source/source.go."))
	case m.busy:
		lines = append(lines, stDim.Render("searching…"))
	case len(m.notes) > 0:
		lines = append(lines, stWarn.Render("warning: "+strings.Join(m.notes, "; ")))
	case m.lastQuery != "" && len(m.results) == 0:
		lines = append(lines, "No results.")
	default:
		lines = append(lines, "")
	}
	listH := h - len(lines)
	if len(m.results) == 0 {
		return strings.Join(lines, "\n")
	}

	detailW := 0
	if w >= 90 {
		detailW = max(coverCols, 34)
	}
	listW := w - detailW
	if detailW > 0 {
		listW--
	}
	list := listLines(len(m.results), m.cursor, listW, listH, !m.typing, func(i int) string {
		return m.row(i, listW)
	})
	if detailW == 0 {
		return strings.Join(append(lines, list...), "\n")
	}
	return strings.Join(lines, "\n") + "\n" +
		columns(listH, column{list, listW}, column{m.detail(detailW), detailW})
}

func (m *searchScreen) row(i, w int) string {
	r := m.results[i]
	it := r.item
	flag := r.dup.Symbol()
	if r.dup == library.NotInLibrary && m.queued[r.ref().String()] {
		flag = "↓"
	}
	right := fmt.Sprintf(" %5s %-9s %1s", clock(it.Duration()), it.Quality.String(), map[bool]string{true: "E"}[it.Explicit])
	name := it.Artist + " — " + it.Title
	switch {
	case m.lastKind == source.KindTrack && it.Album != "":
		name += stDim.Render("  [" + it.Album + "]")
	case m.lastKind == source.KindAlbum && it.Year > 0:
		name += fmt.Sprintf(" (%d)", it.Year)
	}
	return flag + " " + fit(name, max(1, w-2-len([]rune(right)))) + right
}

func (m *searchScreen) detail(w int) []string {
	r := m.results[m.cursor]
	it := r.item
	lines := m.s.covers.view(coverKeyURL(it.CoverURL, m.s.d.Images != nil))
	lines = append(lines, "",
		stBold.Render(it.Title),
		it.Artist,
	)
	if it.Album != "" {
		lines = append(lines, field("Album", it.Album))
	}
	if it.Year > 0 {
		lines = append(lines, field("Year", fmt.Sprint(it.Year)))
	}
	if it.TrackCount > 0 {
		lines = append(lines, field("Tracks", fmt.Sprint(it.TrackCount)))
	}
	if d := clock(it.Duration()); d != "" {
		lines = append(lines, field("Length", d))
	}
	lines = append(lines, field("Quality", orDash(it.Quality.String())))
	if it.Explicit {
		lines = append(lines, field("Explicit", "yes"))
	}
	lines = append(lines, field("Source", r.ref().String()))
	switch r.dup {
	case library.InLibrary:
		lines = append(lines, "", stOK.Render("✓ in library"))
		lines = append(lines, wrapPath(r.dupPath, w)...)
	case library.OtherEdition:
		lines = append(lines, "", stWarn.Render("≈ other edition in library"))
		lines = append(lines, wrapPath(r.dupPath, w)...)
	}
	return lines
}

func coverKeyURL(url string, enabled bool) string {
	if url == "" || !enabled {
		return ""
	}
	return "url:" + url
}

func clock(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	s := int(d.Seconds())
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
