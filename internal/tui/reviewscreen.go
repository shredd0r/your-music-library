package tui

import (
	"fmt"
	"path"
	"strings"

	tea "charm.land/bubbletea/v2"

	"your-music-library/internal/library"
	"your-music-library/internal/metadata"
)

type (
	reviewsMsg struct {
		reviews []library.Review
		entries map[string]library.Entry
		err     error
	}
	resolvePlanMsg struct {
		id    int64
		match library.AlbumMatch
		err   error
	}
	resolveDoneMsg struct {
		id  int64
		rep library.IdentifyReport
		err error
	}
	skipDoneMsg struct {
		id  int64
		err error
	}
)

// reviewScreen shows albums without a confident match: the files next to
// each candidate release, and applies the chosen one after a preview.
type reviewScreen struct {
	s           *shared
	reviews     []library.Review
	entries     map[string]library.Entry
	cursor      int
	cand        int
	preview     *library.AlbumMatch
	confirmSkip bool
	busy        string
	err         error
}

func newReview(s *shared) *reviewScreen { return &reviewScreen{s: s} }

func (m *reviewScreen) name() string { return "Review" }

func (m *reviewScreen) init() tea.Cmd { return m.load() }

func (m *reviewScreen) capturing() bool { return m.preview != nil || m.confirmSkip }

func (m *reviewScreen) help() string {
	switch {
	case m.preview != nil:
		return "y write tags · esc cancel"
	case m.confirmSkip:
		return "y skip this album · any other key cancels"
	}
	return "↑↓ album · ←→ candidate · enter preview · s skip"
}

func (m *reviewScreen) load() tea.Cmd {
	s := m.s
	return func() tea.Msg {
		rs, err := s.d.Lib.Reviews(s.ctx)
		if err != nil {
			return reviewsMsg{err: err}
		}
		es, err := s.d.Lib.Entries(s.ctx)
		if err != nil {
			return reviewsMsg{err: err}
		}
		byPath := make(map[string]library.Entry, len(es))
		for _, e := range es {
			byPath[e.Path] = e
		}
		return reviewsMsg{reviews: rs, entries: byPath}
	}
}

func (m *reviewScreen) current() *library.Review {
	if m.cursor >= len(m.reviews) {
		return nil
	}
	return &m.reviews[m.cursor]
}

func (m *reviewScreen) candidate() *metadata.Match {
	r := m.current()
	if r == nil || m.cand >= len(r.Candidates) {
		return nil
	}
	return &r.Candidates[m.cand]
}

func (m *reviewScreen) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case reviewsMsg:
		m.err = msg.err
		if msg.err != nil {
			return nil
		}
		var id int64
		if r := m.current(); r != nil {
			id = r.ID
		}
		m.reviews, m.entries = msg.reviews, msg.entries
		m.cursor, m.cand = 0, 0
		for i, r := range m.reviews {
			if r.ID == id {
				m.cursor = i
			}
		}
		return m.wantCover()
	case refreshMsg:
		if msg.reviews {
			return m.load()
		}
	case resolvePlanMsg:
		m.busy = ""
		if msg.err != nil {
			return errStatus(msg.err)
		}
		m.preview = &msg.match
	case resolveDoneMsg:
		m.busy, m.preview = "", nil
		if msg.err != nil {
			return tea.Batch(errStatus(msg.err), m.load())
		}
		text := fmt.Sprintf("review #%d: tagged %d files", msg.id, msg.rep.Changed)
		failed := len(msg.rep.Failed) > 0
		if failed {
			text += fmt.Sprintf(", %d failed (%v); the review stays open", len(msg.rep.Failed), msg.rep.Failed[0].Err)
		} else if len(msg.rep.Warnings) > 0 {
			text += "; warning: " + msg.rep.Warnings[0]
		}
		return tea.Batch(func() tea.Msg { return statusMsg{text: text, err: failed} }, refresh(false, true, true))
	case skipDoneMsg:
		m.busy = ""
		if msg.err != nil {
			return errStatus(msg.err)
		}
		return tea.Batch(status("review #%d skipped", msg.id), m.load())
	case tea.KeyPressMsg:
		if m.busy != "" {
			return nil
		}
		return m.key(msg.String())
	}
	return nil
}

func (m *reviewScreen) key(key string) tea.Cmd {
	r := m.current()
	if m.confirmSkip {
		m.confirmSkip = false
		if key != "y" || r == nil {
			return status("not skipped")
		}
		m.busy = "skipping…"
		s, id := m.s, r.ID
		return func() tea.Msg { return skipDoneMsg{id: id, err: s.d.Lib.SkipReview(s.ctx, id)} }
	}
	if m.preview != nil {
		switch key {
		case "y":
			m.busy = "writing tags…"
			s, id, am := m.s, r.ID, *m.preview
			return func() tea.Msg {
				rep, err := s.d.Lib.ApplyResolve(s.ctx, id, am, s.preset(), s.d.Images)
				return resolveDoneMsg{id: id, rep: rep, err: err}
			}
		case "esc", "n":
			m.preview = nil
		}
		return nil
	}
	if r == nil {
		return nil
	}
	if c, ok := move(m.cursor, len(m.reviews), key, 5); ok {
		m.cursor, m.cand = c, 0
		return m.wantCover()
	}
	switch key {
	case "left", "h":
		m.cand = max(0, m.cand-1)
		return m.wantCover()
	case "right", "l":
		m.cand = max(0, min(m.cand+1, len(r.Candidates)-1))
		return m.wantCover()
	case "enter":
		if len(r.Candidates) == 0 {
			return status("no candidates; skip it or tag the files on the Library screen")
		}
		m.busy = "preparing preview…"
		s, id, n := m.s, r.ID, m.cand+1
		return func() tea.Msg {
			am, err := s.d.Lib.PlanResolve(s.ctx, id, n, s.d.Identifier)
			return resolvePlanMsg{id: id, match: am, err: err}
		}
	case "s":
		m.confirmSkip = true
	}
	return nil
}

func (m *reviewScreen) wantCover() tea.Cmd {
	c := m.candidate()
	if c == nil || c.Release.FrontURL == "" || m.s.d.Images == nil {
		return nil
	}
	s, url := m.s, c.Release.FrontURL
	return s.covers.want("url:"+url, func() ([]byte, error) {
		data, _, err := s.d.Images.Fetch(s.ctx, url)
		return data, err
	})
}

func (m *reviewScreen) view(w, h int) string {
	switch {
	case m.err != nil:
		return stErr.Render(m.err.Error())
	case len(m.reviews) == 0:
		return "Nothing to review. Albums that `identify`, `import` or downloads can't match confidently appear here."
	case m.preview != nil:
		return m.previewView(w, h)
	}
	var lines []string
	listH := min(len(m.reviews), max(2, h/4))
	lines = append(lines, listLines(len(m.reviews), m.cursor, w, listH, true, func(i int) string {
		r := m.reviews[i]
		return fmt.Sprintf("#%-3d %s  %s", r.ID, r.Folder,
			stDim.Render(fmt.Sprintf("(%d files, %d candidates)", len(r.Paths), len(r.Candidates))))
	})...)
	lines = append(lines, stDim.Render(strings.Repeat("─", w)))
	switch {
	case m.busy != "":
		lines = append(lines, stDim.Render(m.busy))
	case m.confirmSkip:
		lines = append(lines, stWarn.Render("Skip this album? Its files stay as they are. (y/n)"))
	}
	bodyH := h - len(lines)
	left := m.filesColumn()
	if w < 80 {
		return strings.Join(append(lines, m.candidateColumn(w)...), "\n")
	}
	leftW := w * 2 / 5
	rightW := w - leftW - 1
	return strings.Join(lines, "\n") + "\n" +
		columns(bodyH, column{left, leftW}, column{m.candidateColumn(rightW), rightW})
}

func (m *reviewScreen) filesColumn() []string {
	r := m.current()
	lines := []string{stBold.Render("Your files"),
		field("Looked up", strings.TrimSpace(r.Query.Artist+" — "+orDash(r.Query.Album))), ""}
	for _, p := range r.Paths {
		e, ok := m.entries[p]
		if !ok {
			lines = append(lines, stWarn.Render("missing: ")+path.Base(p))
			continue
		}
		num := "  "
		if e.TrackNumber > 0 {
			num = fmt.Sprintf("%02d", e.TrackNumber)
		}
		lines = append(lines, stDim.Render(num)+" "+orDash(e.Title)+stDim.Render(" · "+e.Artist))
	}
	return lines
}

func (m *reviewScreen) candidateColumn(w int) []string {
	r := m.current()
	if len(r.Candidates) == 0 {
		return []string{stBold.Render("No candidates found")}
	}
	c := r.Candidates[m.cand]
	rel := c.Release
	head := []string{
		stBold.Render(fmt.Sprintf("Candidate %d of %d", m.cand+1, len(r.Candidates))) +
			stDim.Render(fmt.Sprintf("  score %.2f", c.Score)),
		rel.Artist + " — " + rel.Title,
	}
	var details []string
	for _, d := range []string{rel.Date, rel.ReleaseType, rel.Format, rel.Country, rel.Label} {
		if d != "" {
			details = append(details, d)
		}
	}
	head = append(head, strings.Join(details, " · "), stDim.Render(rel.Provider+" "+rel.URL))
	var lines []string
	key := ""
	if rel.FrontURL != "" && m.s.d.Images != nil {
		key = "url:" + rel.FrontURL
	}
	if key != "" && w >= coverCols+24 {
		lines = splitLines(columns(coverRows, column{m.s.covers.view(key), coverCols}, column{head, w - coverCols - 1}))
	} else {
		lines = head
	}
	lines = append(lines, "")
	for _, t := range rel.Tracks {
		num := fmt.Sprintf("%02d", t.Number)
		if t.Disc > 1 {
			num = fmt.Sprintf("%d-%02d", t.Disc, t.Number)
		}
		line := stDim.Render(num) + " " + t.Title
		if d := clock(t.Duration); d != "" {
			line += stDim.Render(" " + d)
		}
		lines = append(lines, line)
	}
	return lines
}

func (m *reviewScreen) previewView(w, h int) string {
	am := m.preview
	rel := am.Release
	lines := []string{
		stBold.Render("Preview: ") + fmt.Sprintf("%s — %s (%s), score %.2f, %s", rel.Artist, rel.Title,
			orDash(rel.Date), am.Score, rel.Provider),
		"",
	}
	if m.busy != "" {
		lines = append(lines, stDim.Render(m.busy))
	}
	for _, it := range am.Items {
		lines = append(lines, it.Path)
		for _, c := range it.Changes {
			lines = append(lines, "    "+changeLine(c.Field, c.Old, c.New))
		}
	}
	if rel.FrontURL != "" {
		lines = append(lines, "    + cover from "+rel.FrontURL)
	}
	for _, p := range am.Unmatched {
		lines = append(lines, stWarn.Render("? "+p+": no matching track on this release (left unchanged)"))
	}
	if len(lines) > h {
		lines = append(lines[:h-1], stDim.Render(fmt.Sprintf("… %d more lines", len(lines)-h+1)))
	}
	return strings.Join(lines, "\n")
}
