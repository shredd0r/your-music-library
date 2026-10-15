// Package tui is the terminal UI, built with Bubble Tea. Its screens call
// the same internal packages as the CLI; no library logic lives here.
//
// Screens: Search & Download, Queue, Library, Review, Settings. Long work
// (searches, lookups, tag writes, downloads) runs in commands, and the
// results come back as messages.
package tui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"your-music-library/internal/config"
	"your-music-library/internal/download"
	"your-music-library/internal/library"
	"your-music-library/internal/metadata"
	"your-music-library/internal/queue"
	"your-music-library/internal/source"
	"your-music-library/internal/termimg"
)

// Deps is what the UI works with. The caller owns and closes them.
type Deps struct {
	Lib        *library.Library
	Queue      *queue.Queue
	Sources    *source.Registry
	Identifier *metadata.Identifier // nil: no lookups; downloads keep the source's tags
	Images     library.ImageSource  // nil: no remote covers
	Config     config.Config
	ConfigPath string
	SourcesDir string
	Covers     termimg.Protocol // how covers are drawn
	// Warnings are shown in the status line at start.
	Warnings []string
	// Verify overrides download verification (tests); nil = download.Verify.
	Verify download.Verifier
	// Backoff overrides the download retry delays (tests).
	Backoff func(attempt int) time.Duration
	// Refresh reloads the queue this often while downloads run; 0 = only
	// on download events.
	Refresh time.Duration
}

// Messages shared by screens.
type (
	statusMsg struct {
		text string
		err  bool
	}
	// refreshMsg asks screens to reload data that may have changed.
	refreshMsg struct{ queue, library, reviews bool }
	queueMsg   struct {
		jobs []queue.Job
		err  error
	}
	runnerLogMsg  string
	runnerDoneMsg struct {
		sum download.Summary
		err error
	}
	pendingMsg struct{ n int }
	tickMsg    struct{}
)

func status(format string, args ...any) tea.Cmd {
	text := fmt.Sprintf(format, args...)
	return func() tea.Msg { return statusMsg{text: text} }
}

func errStatus(err error) tea.Cmd {
	return func() tea.Msg { return statusMsg{text: err.Error(), err: true} }
}

func refresh(queue, library, reviews bool) tea.Cmd {
	return func() tea.Msg { return refreshMsg{queue: queue, library: library, reviews: reviews} }
}

// shared is state every screen can use.
type shared struct {
	ctx    context.Context
	d      Deps
	cfg    config.Config
	covers *covers

	dl struct {
		running bool
		cancel  context.CancelFunc
		events  chan tea.Msg
		log     []string
	}
	counts struct{ pending, running, failed, reviews int }
}

func (s *shared) preset() config.Preset {
	p, _ := s.cfg.Preset(s.cfg.ActivePreset)
	return p
}

const maxLog = 200

// startDownloads runs the queue in the background unless it is running.
// Events arrive as runnerLogMsg and finally runnerDoneMsg.
func (s *shared) startDownloads() tea.Cmd {
	if s.dl.running {
		return nil
	}
	ctx, cancel := context.WithCancel(s.ctx)
	events := make(chan tea.Msg, 64)
	r := &download.Runner{
		Queue: s.d.Queue, Sources: s.d.Sources, Lib: s.d.Lib,
		Identifier: s.d.Identifier, Images: s.d.Images, Preset: s.preset(),
		Parallel: s.cfg.Queue.Parallel, Verify: s.d.Verify, Backoff: s.d.Backoff,
		Log: func(format string, args ...any) { events <- runnerLogMsg(fmt.Sprintf(format, args...)) },
	}
	s.dl.running, s.dl.cancel, s.dl.events = true, cancel, events
	go func() {
		sum, err := r.Run(ctx)
		cancel()
		events <- runnerDoneMsg{sum: sum, err: err}
		close(events)
	}()
	return tea.Batch(waitEvent(events), s.tick())
}

func waitEvent(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		m, ok := <-ch
		if !ok {
			return nil
		}
		return m
	}
}

func (s *shared) tick() tea.Cmd {
	if s.d.Refresh <= 0 {
		return nil
	}
	return tea.Tick(s.d.Refresh, func(time.Time) tea.Msg { return tickMsg{} })
}

// stopDownloads cancels a running queue; unfinished jobs resume later.
func (s *shared) stopDownloads() {
	if s.dl.running {
		s.dl.cancel()
	}
}

// waitDownloads stops the queue and waits until the runner has finished.
func (s *shared) waitDownloads() {
	if s.dl.events == nil {
		return
	}
	s.dl.cancel()
	for range s.dl.events { // drain until the runner closes the channel
	}
}

func loadQueue(s *shared) tea.Cmd {
	return func() tea.Msg {
		jobs, err := s.d.Queue.List(s.ctx)
		return queueMsg{jobs: jobs, err: err}
	}
}

// screen is one tab of the UI.
type screen interface {
	name() string
	init() tea.Cmd
	update(msg tea.Msg) tea.Cmd
	view(w, h int) string
	// capturing reports that keys go to the screen (text input, dialogs),
	// so global shortcuts like q and 1-5 are off.
	capturing() bool
	help() string
}

// App is the root model.
type App struct {
	s             *shared
	screens       []screen
	active        int
	width, height int
	status        statusMsg
	quitting      bool
}

// New returns the root model.
func New(ctx context.Context, d Deps) *App {
	s := &shared{ctx: ctx, d: d, cfg: d.Config,
		covers: newCovers(d.Covers, uint32(time.Now().UnixNano()%0xfff000)+1)}
	a := &App{s: s}
	a.screens = []screen{newSearch(s), newQueueScreen(s), newLibrary(s), newReview(s), newSettings(s)}
	if len(d.Warnings) > 0 {
		a.status = statusMsg{text: "warning: " + strings.Join(d.Warnings, "; "), err: true}
	}
	return a
}

// Init loads every screen's data.
func (a *App) Init() tea.Cmd {
	cmds := []tea.Cmd{loadQueue(a.s)}
	for _, sc := range a.screens {
		cmds = append(cmds, sc.init())
	}
	return tea.Batch(cmds...)
}

// Update handles global keys and messages, then passes messages on: keys
// to the active screen, everything else to all screens.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = m.Width, m.Height
	case statusMsg:
		a.status = m
		return a, nil
	case coverMsg:
		return a, a.s.covers.update(m)
	case runnerLogMsg:
		a.s.dl.log = append(a.s.dl.log, string(m))
		if n := len(a.s.dl.log); n > maxLog {
			a.s.dl.log = a.s.dl.log[n-maxLog:]
		}
		cmds = append(cmds, waitEvent(a.s.dl.events), loadQueue(a.s))
		if strings.HasPrefix(string(m), "✓") || strings.HasPrefix(string(m), "= ") {
			cmds = append(cmds, refresh(false, true, true))
		}
	case runnerDoneMsg:
		a.s.dl.running = false
		if a.quitting {
			return a, tea.Quit
		}
		switch {
		case m.err != nil && a.s.ctx.Err() == nil && !isCanceled(m.err):
			cmds = append(cmds, errStatus(fmt.Errorf("downloads stopped: %w", m.err)))
		default:
			cmds = append(cmds, status("downloads: imported %d, already in library %d, failed %d",
				m.sum.Imported, m.sum.Skipped, m.sum.Failed))
			if m.err == nil {
				cmds = append(cmds, a.checkPending())
			}
		}
		cmds = append(cmds, refresh(true, true, true))
	case pendingMsg:
		// jobs added just as the last run finished
		if m.n > 0 && !a.s.dl.running {
			return a, a.s.startDownloads()
		}
		return a, nil
	case tickMsg:
		if !a.s.dl.running {
			return a, nil
		}
		return a, tea.Batch(loadQueue(a.s), a.s.tick())
	case queueMsg:
		a.countJobs(m.jobs)
	case reviewsMsg:
		a.s.counts.reviews = len(m.reviews)
	case tea.KeyPressMsg:
		if cmd, done := a.globalKey(m); done {
			return a, cmd
		}
		return a, a.screens[a.active].update(m)
	}
	for _, sc := range a.screens {
		cmds = append(cmds, sc.update(msg))
	}
	return a, tea.Batch(cmds...)
}

func isCanceled(err error) bool {
	return err == context.Canceled || strings.Contains(err.Error(), context.Canceled.Error())
}

func (a *App) checkPending() tea.Cmd {
	s := a.s
	return func() tea.Msg {
		jobs, err := s.d.Queue.List(s.ctx)
		if err != nil {
			return nil
		}
		n := 0
		for _, j := range jobs {
			if j.Status == queue.Pending {
				n++
			}
		}
		return pendingMsg{n: n}
	}
}

func (a *App) countJobs(jobs []queue.Job) {
	c := &a.s.counts
	c.pending, c.running, c.failed = 0, 0, 0
	for _, j := range jobs {
		if j.Kind != source.KindTrack {
			continue
		}
		switch j.Status {
		case queue.Pending:
			c.pending++
		case queue.Running:
			c.running++
		case queue.Failed:
			c.failed++
		}
	}
}

func (a *App) globalKey(k tea.KeyPressMsg) (tea.Cmd, bool) {
	key := k.String()
	if key == "ctrl+c" {
		return a.quit(), true
	}
	if a.screens[a.active].capturing() {
		return nil, false
	}
	switch key {
	case "q":
		return a.quit(), true
	case "tab":
		a.active = (a.active + 1) % len(a.screens)
		return nil, true
	case "shift+tab":
		a.active = (a.active + len(a.screens) - 1) % len(a.screens)
		return nil, true
	}
	if len(key) == 1 && key[0] >= '1' && int(key[0]-'1') < len(a.screens) {
		a.active = int(key[0] - '1')
		return nil, true
	}
	return nil, false
}

// quit stops downloads first; the program ends when the runner is done.
func (a *App) quit() tea.Cmd {
	if !a.s.dl.running {
		return tea.Quit
	}
	if a.quitting { // second request: don't wait
		return tea.Quit
	}
	a.quitting = true
	a.s.stopDownloads()
	a.status = statusMsg{text: "stopping downloads… (they resume next time)"}
	return nil
}

// View renders the whole screen.
func (a *App) View() tea.View {
	v := tea.NewView(a.render())
	v.AltScreen = true
	v.WindowTitle = "ymlib"
	return v
}

func (a *App) render() string {
	w, h := a.width, a.height
	if w == 0 || h == 0 {
		return "loading…"
	}
	body := max(1, h-3)
	sc := a.screens[a.active]
	help := sc.help()
	if !sc.capturing() {
		help += " · tab/1-5 screens · q quit"
	}
	statusLine := a.status.text
	if a.status.err {
		statusLine = stErr.Render(statusLine)
	}
	return strings.Join([]string{
		a.header(w),
		block(splitLines(sc.view(w, body)), w, body),
		fit(statusLine, w),
		fit(stDim.Render(help), w),
	}, "\n")
}

func (a *App) header(w int) string {
	var tabs []string
	for i, sc := range a.screens {
		label := fmt.Sprintf("%d %s", i+1, sc.name())
		switch {
		case i == 1 && a.s.counts.pending+a.s.counts.running > 0:
			label += fmt.Sprintf(" (%d)", a.s.counts.pending+a.s.counts.running)
		case i == 3 && a.s.counts.reviews > 0:
			label += fmt.Sprintf(" (%d)", a.s.counts.reviews)
		}
		if i == a.active {
			tabs = append(tabs, stTabOn.Render(label))
		} else {
			tabs = append(tabs, stTab.Render(label))
		}
	}
	left := stBold.Render("ymlib ") + strings.Join(tabs, "")
	right := ""
	if a.s.dl.running {
		right = stAccent.Render(fmt.Sprintf("↓ %d running, %d waiting", a.s.counts.running, a.s.counts.pending))
	} else if a.s.counts.failed > 0 {
		right = stWarn.Render(fmt.Sprintf("%d failed", a.s.counts.failed))
	}
	gap := w - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		return fit(left, w)
	}
	return left + strings.Repeat(" ", gap) + right
}

// Run starts the UI and blocks until the user quits. Running downloads are
// stopped (they resume next time) and Kitty images are freed on exit.
func Run(ctx context.Context, d Deps, out io.Writer) error {
	a := New(ctx, d)
	opts := []tea.ProgramOption{tea.WithContext(ctx), tea.WithOutput(out)}
	if d.Covers == termimg.Kitty {
		// the image id travels in the 24-bit color of the placeholders, so
		// colors must not be reduced; Kitty and Ghostty have true color
		opts = append(opts, tea.WithColorProfile(colorprofile.TrueColor))
	}
	p := tea.NewProgram(a, opts...)
	_, err := p.Run()
	a.s.waitDownloads()
	if seq := a.s.covers.cleanup(); seq != "" {
		_, _ = io.WriteString(out, seq) // freeing terminal memory is best-effort
	}
	return err
}
