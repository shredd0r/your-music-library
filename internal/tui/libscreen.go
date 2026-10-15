package tui

import (
	"cmp"
	"fmt"
	"path"
	"slices"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"your-music-library/internal/config"
	"your-music-library/internal/library"
)

type libraryMsg struct {
	entries []library.Entry
	scanned *library.ScanReport
	err     error
}

// album is the tracks of one library folder.
type album struct {
	dir               string
	artist, title, yr string
	tracks            []library.Entry
	searchText        string // lower-case artist, album and titles
}

func groupAlbums(es []library.Entry) []album {
	byDir := map[string]*album{}
	var order []string
	for _, e := range es {
		dir := path.Dir(e.Path)
		a := byDir[dir]
		if a == nil {
			a = &album{dir: dir}
			byDir[dir] = a
			order = append(order, dir)
		}
		a.tracks = append(a.tracks, e)
	}
	out := make([]album, 0, len(order))
	for _, dir := range order {
		a := byDir[dir]
		slices.SortStableFunc(a.tracks, func(x, y library.Entry) int {
			return cmp.Or(cmp.Compare(x.DiscNumber, y.DiscNumber), cmp.Compare(x.TrackNumber, y.TrackNumber),
				cmp.Compare(x.Path, y.Path))
		})
		first := a.tracks[0]
		a.artist, a.title = first.EffectiveAlbumArtist(), first.Album
		if y := first.Year(); y > 0 {
			a.yr = fmt.Sprint(y)
		}
		if a.title == "" {
			a.title = path.Base(dir)
		}
		var b strings.Builder
		b.WriteString(a.artist + "\n" + a.title)
		for _, t := range a.tracks {
			b.WriteString("\n" + t.Title)
		}
		a.searchText = strings.ToLower(b.String())
		out = append(out, *a)
	}
	slices.SortStableFunc(out, func(x, y album) int {
		return cmp.Or(cmp.Compare(strings.ToLower(x.artist), strings.ToLower(y.artist)),
			cmp.Compare(x.yr, y.yr), cmp.Compare(strings.ToLower(x.title), strings.ToLower(y.title)))
	})
	return out
}

// libScreen browses albums and tracks, shows tags and covers and edits tags.
type libScreen struct {
	s         *shared
	albums    []album
	shown     []int // indexes into albums matching the filter
	cursor    int   // into shown
	tcursor   int
	onTracks  bool
	filter    textinput.Model
	filtering bool
	form      *editForm
	loading   bool
	err       error
}

func newLibrary(s *shared) *libScreen {
	return &libScreen{s: s, filter: newInput("Filter: ")}
}

func (m *libScreen) name() string { return "Library" }

func (m *libScreen) init() tea.Cmd { return m.load(true) }

func (m *libScreen) capturing() bool { return m.filtering || m.form != nil }

func (m *libScreen) help() string {
	switch {
	case m.form != nil:
		return m.form.help()
	case m.filtering:
		return "type to filter · enter done · esc clear"
	case m.onTracks:
		return "↑↓ track · ←/esc albums · e edit track · s rescan"
	}
	return "↑↓ album · →/enter tracks · e edit album · / filter · s rescan"
}

// load reads the index, scanning the library folder first if scan is set.
func (m *libScreen) load(scan bool) tea.Cmd {
	m.loading = true
	s := m.s
	return func() tea.Msg {
		var rep *library.ScanReport
		if scan {
			r, err := s.d.Lib.Scan(s.ctx)
			if err != nil {
				return libraryMsg{err: err}
			}
			rep = &r
		}
		es, err := s.d.Lib.Entries(s.ctx)
		return libraryMsg{entries: es, scanned: rep, err: err}
	}
}

func (m *libScreen) update(msg tea.Msg) tea.Cmd {
	if m.form != nil {
		switch msg.(type) {
		case tea.KeyPressMsg, editPlanMsg, editDoneMsg:
			cmd := m.form.update(msg)
			if m.form.done {
				m.form = nil
			}
			return cmd
		}
	}
	switch msg := msg.(type) {
	case libraryMsg:
		return m.loaded(msg)
	case refreshMsg:
		if msg.library {
			return m.load(false)
		}
	case tea.KeyPressMsg:
		if m.filtering {
			return m.filterKey(msg)
		}
		return m.key(msg.String())
	}
	return nil
}

func (m *libScreen) loaded(msg libraryMsg) tea.Cmd {
	m.loading = false
	m.err = msg.err
	if msg.err != nil {
		return errStatus(msg.err)
	}
	var selected string
	if a := m.current(); a != nil {
		selected = a.dir
	}
	m.albums = groupAlbums(msg.entries)
	m.applyFilter()
	for i, ai := range m.shown {
		if m.albums[ai].dir == selected {
			m.cursor = i
		}
	}
	m.tcursor = 0
	var cmds []tea.Cmd
	if r := msg.scanned; r != nil && r.Added+r.Updated+r.Removed > 0 {
		cmds = append(cmds, status("scan: %d added, %d updated, %d removed", r.Added, r.Updated, r.Removed))
	}
	return tea.Batch(append(cmds, m.wantCover())...)
}

func (m *libScreen) applyFilter() {
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	m.shown = m.shown[:0]
	for i, a := range m.albums {
		if q == "" || strings.Contains(a.searchText, q) {
			m.shown = append(m.shown, i)
		}
	}
	m.cursor = max(0, min(m.cursor, len(m.shown)-1))
}

func (m *libScreen) current() *album {
	if m.cursor >= len(m.shown) {
		return nil
	}
	return &m.albums[m.shown[m.cursor]]
}

func (m *libScreen) filterKey(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "enter":
		m.filtering = false
		m.filter.Blur()
		return m.wantCover()
	case "esc":
		m.filtering = false
		m.filter.Blur()
		m.filter.SetValue("")
		m.applyFilter()
		return m.wantCover()
	}
	var cmd tea.Cmd
	m.filter, cmd = m.filter.Update(k)
	m.applyFilter()
	return cmd
}

func (m *libScreen) key(key string) tea.Cmd {
	a := m.current()
	if m.onTracks && a != nil {
		if c, ok := move(m.tcursor, len(a.tracks), key, 10); ok {
			m.tcursor = c
			return nil
		}
		switch key {
		case "left", "h", "esc":
			m.onTracks = false
			return nil
		}
	} else if c, ok := move(m.cursor, len(m.shown), key, 10); ok {
		m.cursor, m.tcursor = c, 0
		return m.wantCover()
	}
	switch key {
	case "right", "l", "enter":
		if a != nil {
			m.onTracks = true
		}
	case "/":
		m.filtering = true
		return m.filter.Focus()
	case "s":
		return tea.Batch(status("scanning…"), m.load(true))
	case "e":
		return m.startEdit()
	}
	return nil
}

func (m *libScreen) startEdit() tea.Cmd {
	a := m.current()
	if a == nil {
		return nil
	}
	p := m.s.preset()
	fields, tracks := library.AlbumFields, a.tracks
	title := fmt.Sprintf("Edit album %s — %s (%d tracks)", a.artist, a.title, len(a.tracks))
	if m.onTracks {
		fields, tracks = library.EditableFields, a.tracks[m.tcursor:m.tcursor+1]
		title = "Edit track " + tracks[0].Path
	}
	var shown []config.Field
	for _, f := range fields {
		if p.Has(f) {
			shown = append(shown, f)
		}
	}
	if len(shown) == 0 {
		return status("preset %q writes none of these fields", p.Name)
	}
	var cmd tea.Cmd
	m.form, cmd = newEditForm(m.s, title, tracks, shown)
	return cmd
}

func coverKeyLib(e library.Entry) string { return fmt.Sprintf("lib:%s@%d", e.Path, e.MtimeNs) }

func (m *libScreen) wantCover() tea.Cmd {
	a := m.current()
	if a == nil {
		return nil
	}
	s, e := m.s, a.tracks[0]
	return s.covers.want(coverKeyLib(e), func() ([]byte, error) { return s.d.Lib.Cover(e.Path) })
}

func (m *libScreen) view(w, h int) string {
	if m.form != nil {
		return m.form.view(w, h)
	}
	var top []string
	if m.filtering || m.filter.Value() != "" {
		top = append(top, m.filter.View())
	}
	switch {
	case m.loading && len(m.albums) == 0:
		return strings.Join(append(top, stDim.Render("reading the library…")), "\n")
	case len(m.albums) == 0:
		return strings.Join(append(top, "The library is empty. Import music with `ymlib import` or download it on the Search screen."), "\n")
	case len(m.shown) == 0:
		return strings.Join(append(top, "No album matches the filter."), "\n")
	}
	bodyH := h - len(top)
	albumW := min(max(30, w*2/5), w)
	if w < 70 {
		albumW = w
	}
	albums := listLines(len(m.shown), m.cursor, albumW, bodyH, !m.onTracks, func(i int) string {
		a := m.albums[m.shown[i]]
		yr := ""
		if a.yr != "" {
			yr = stDim.Render(" " + a.yr)
		}
		return a.artist + " — " + a.title + yr
	})
	if w < 70 {
		return strings.Join(append(top, albums...), "\n")
	}
	rightW := w - albumW - 1
	right := m.detail(rightW, bodyH)
	out := columns(bodyH, column{albums, albumW}, column{right, rightW})
	if len(top) > 0 {
		return strings.Join(top, "\n") + "\n" + out
	}
	return out
}

func (m *libScreen) detail(w, h int) []string {
	a := m.current()
	cover := m.s.covers.view(coverKeyLib(a.tracks[0]))
	info := []string{stBold.Render(a.title), a.artist}
	if a.yr != "" {
		info = append(info, a.tracks[0].Date)
	}
	if g := a.tracks[0].Genre; g != "" {
		info = append(info, g)
	}
	info = append(info, stDim.Render(fmt.Sprintf("%d tracks", len(a.tracks))), stDim.Render(a.dir))
	var lines []string
	if w >= coverCols+20 {
		infoW := w - coverCols - 1
		lines = splitLines(columns(coverRows, column{cover, coverCols}, column{info, infoW}))
	} else {
		lines = info
	}
	lines = append(lines, "")

	t := a.tracks[min(m.tcursor, len(a.tracks)-1)]
	tagLines := []string{
		field("Title", t.Title), field("Artist", t.Artist), field("Artists", strings.Join(t.Artists, "; ")),
		field("Album Artist", t.AlbumArtist), field("Date", t.Date),
		field("Track", library.FieldValue(t.Track, config.FieldTrackNumber)),
		field("Disc", library.FieldValue(t.Track, config.FieldDiscNumber)),
		field("Genre", t.Genre), field("File", path.Base(t.Path)),
		field("Size", fmt.Sprintf("%.1f MB", float64(t.Size)/1e6)),
	}
	if id := cmp.Or(t.IDs.DiscogsReleaseID, t.IDs.MBReleaseID); id != "" {
		tagLines = append(tagLines, field("Release ID", id))
	}
	tracksH := max(3, h-len(lines)-len(tagLines)-1)
	tracks := listLines(len(a.tracks), m.tcursor, w, tracksH, m.onTracks, func(i int) string {
		e := a.tracks[i]
		num := fmt.Sprintf("%02d", e.TrackNumber)
		if e.DiscTotal > 1 || e.DiscNumber > 1 {
			num = fmt.Sprintf("%d-%02d", max(1, e.DiscNumber), e.TrackNumber)
		}
		title := e.Title
		if e.Artist != "" && e.Artist != a.artist {
			title += stDim.Render(" · " + e.Artist)
		}
		return stDim.Render(num) + " " + title
	})
	for len(tracks) < tracksH {
		tracks = append(tracks, "")
	}
	lines = append(lines, tracks...)
	lines = append(lines, stDim.Render(strings.Repeat("─", w)))
	return append(lines, tagLines...)
}
