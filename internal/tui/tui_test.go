package tui

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"your-music-library/internal/config"
	"your-music-library/internal/download"
	"your-music-library/internal/library"
	"your-music-library/internal/metadata"
	"your-music-library/internal/queue"
	"your-music-library/internal/source"
	"your-music-library/internal/store"
	"your-music-library/internal/tags"
	"your-music-library/internal/termimg"
	"your-music-library/internal/testutil"
)

// harness drives the App like Bubble Tea does: messages go to Update, and
// returned commands run (synchronously) with their messages fed back.
type harness struct {
	t       *testing.T
	app     *App
	lib     *library.Library
	db      *sql.DB
	cfgPath string
	quit    bool
}

func newHarness(t *testing.T, prepare func(h *harness)) *harness {
	t.Helper()
	root := filepath.Join(t.TempDir(), "music")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	lib, err := library.Open(root, db, config.DefaultPathTemplate)
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("# test config\nlibrary_path: "+root+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	srcDir := testutil.FakeSourceDir(t)
	plugins, err := source.Discover(srcDir)
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, lib: lib, db: db, cfgPath: cfgPath}
	if prepare != nil {
		prepare(h)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h.app = New(ctx, Deps{
		Lib: lib, Queue: queue.New(db), Sources: source.NewRegistry(plugins),
		Config: cfg, ConfigPath: cfgPath, SourcesDir: srcDir, Covers: termimg.HalfBlock,
		// the fake plugin's "long" track claims 5 minutes
		Verify: func(_ context.Context, _ string, expected time.Duration) error {
			if expected > time.Minute {
				return download.Permanent(errors.New("length 0:00, expected 5:00"))
			}
			return nil
		},
		Backoff: func(int) time.Duration { return 0 },
	})
	t.Cleanup(h.app.s.waitDownloads)
	h.send(tea.WindowSizeMsg{Width: 140, Height: 40})
	h.run(h.app.Init())
	return h
}

func (h *harness) send(msg tea.Msg) {
	h.t.Helper()
	_, cmd := h.app.Update(msg)
	h.run(cmd)
}

func (h *harness) run(cmd tea.Cmd) {
	h.t.Helper()
	pending := []tea.Cmd{cmd}
	for len(pending) > 0 {
		c := pending[0]
		pending = pending[1:]
		if c == nil {
			continue
		}
		msg := h.exec(c)
		switch m := msg.(type) {
		case nil, tea.RawMsg:
		case tea.BatchMsg:
			pending = append(pending, m...)
		case tea.QuitMsg:
			h.quit = true
		default:
			_, next := h.app.Update(m)
			pending = append(pending, next)
		}
	}
}

func (h *harness) exec(c tea.Cmd) tea.Msg {
	h.t.Helper()
	done := make(chan tea.Msg, 1)
	go func() { done <- c() }()
	select {
	case m := <-done:
		return m
	case <-time.After(30 * time.Second):
		h.t.Fatal("command did not finish")
		return nil
	}
}

func key(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "ctrl+t":
		return tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl}
	case "ctrl+u":
		return tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: s}
}

func (h *harness) keys(keys ...string) {
	h.t.Helper()
	for _, k := range keys {
		h.send(key(k))
	}
}

func (h *harness) typeText(s string) {
	h.t.Helper()
	for _, r := range s {
		h.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// screen returns the rendered screen without escape sequences.
func (h *harness) screen() string { return ansi.Strip(h.app.View().Content) }

func (h *harness) wantScreen(parts ...string) {
	h.t.Helper()
	s := h.screen()
	for _, p := range parts {
		if !strings.Contains(s, p) {
			h.t.Fatalf("screen lacks %q:\n%s", p, s)
		}
	}
}

func (h *harness) status() string { return h.app.status.text }

// addTrack writes a tagged file into the library and indexes it.
func (h *harness) addTrack(rel string, tr tags.Track) {
	h.t.Helper()
	abs := testutil.CopyAudio(h.t, strings.TrimPrefix(filepath.Ext(rel), "."), filepath.Join(h.lib.Root(), rel))
	if err := tags.Write(abs, tr); err != nil {
		h.t.Fatal(err)
	}
	if _, err := h.lib.Scan(context.Background()); err != nil {
		h.t.Fatal(err)
	}
}

func TestSearchAndDownloadTrack(t *testing.T) {
	h := newHarness(t, nil)
	h.wantScreen("1 Search", "Search tracks:")
	h.keys("/")
	h.typeText("rock")
	h.keys("enter")
	h.wantScreen("Michael Jackson — Rock With You", "[Off the Wall]", "FLAC 16/44.1", "fake:t1")

	h.keys("enter") // download the selected result; the queue runs to the end
	if !strings.Contains(h.status(), "imported 1") {
		t.Fatalf("status = %q", h.status())
	}
	es, err := h.lib.Entries(context.Background())
	if err != nil || len(es) != 1 || es[0].Title != "Rock With You" {
		t.Fatalf("library = %+v, %v", es, err)
	}

	h.keys("/")
	h.typeText("rock")
	h.keys("enter")
	h.wantScreen("✓ Michael Jackson — Rock With You", "✓ in library", "Michael Jackson/Off the Wall/02 - ", "Rock With You.flac")
	h.keys("enter")
	if !strings.Contains(h.status(), "already in the library") {
		t.Errorf("downloading a track in the library: status %q", h.status())
	}

	h.keys("3")
	h.wantScreen("Michael Jackson — Off the Wall", "Rock With You")
}

func TestSearchAlbumsDownloadsAllTracks(t *testing.T) {
	h := newHarness(t, nil)
	h.keys("ctrl+t")
	h.wantScreen("Search albums:")
	h.keys("/")
	h.typeText("off the wall")
	h.keys("enter")
	h.wantScreen("Michael Jackson — Off the Wall (1979)")
	h.keys("d")
	if !strings.Contains(h.status(), "imported 2") {
		t.Fatalf("status = %q", h.status())
	}
	h.keys("2")
	h.wantScreen("Downloads idle", "album Michael Jackson — Off the Wall", "2 tracks", "Rock With You")
}

func TestTypingDoesNotSwitchScreens(t *testing.T) {
	h := newHarness(t, nil)
	h.keys("enter") // enter also starts a search
	h.typeText("2q")
	if h.app.active != 0 || h.quit {
		t.Fatalf("active = %d, quit = %v", h.app.active, h.quit)
	}
	h.wantScreen("Search tracks: 2q")
	h.keys("tab") // typing: tab is not a screen switch here
	if h.app.active != 0 {
		t.Errorf("tab switched screens while typing")
	}
	h.keys("esc", "tab") // esc leaves the search box
	if h.app.active != 1 {
		t.Errorf("active = %d after esc, tab", h.app.active)
	}
	h.keys("ctrl+c")
	if !h.quit {
		t.Error("ctrl+c did not quit")
	}
}

func TestQueueFailedJobRetryAndClear(t *testing.T) {
	h := newHarness(t, nil)
	h.keys("/")
	h.typeText("long")
	h.keys("enter", "enter")
	if !strings.Contains(h.status(), "failed 1") {
		t.Fatalf("status = %q", h.status())
	}
	h.keys("2")
	h.wantScreen("✗", "Nobody — Long Song", "attempt 1: length 0:00, expected 5:00", "1 failed")

	h.keys("r") // retried: fails again
	jobs, _ := queue.New(h.db).List(context.Background())
	if len(jobs) != 1 || jobs[0].Status != queue.Failed {
		t.Fatalf("jobs = %+v", jobs)
	}
	h.keys("c")
	h.wantScreen("Nobody — Long Song")
	h.keys("C")
	h.wantScreen("The queue is empty.")
}

func TestLibraryEditAlbum(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.addTrack("Artist/Alb/01.flac", tags.Track{Title: "One", Album: "Alb", Artist: "Artist", TrackNumber: 1})
		h.addTrack("Artist/Alb/02.mp3", tags.Track{Title: "Two", Album: "alb", Artist: "Artist", TrackNumber: 2, Genre: "Rock"})
	})
	h.keys("3")
	h.wantScreen("Artist — Alb", "01 One", "02 Two")

	h.keys("e")
	h.wantScreen("Edit album", "Album:", "Genre:", "(different values; leave empty to keep)")
	// Album differs ("Alb" vs "alb"), so it starts empty
	h.typeText("Album X")
	h.keys("enter")
	h.wantScreen("preview", "Artist/Alb/01.flac", "Album:        Alb → Album X")
	h.keys("y")
	if !strings.Contains(h.status(), "retagged 2 files") {
		t.Fatalf("status = %q", h.status())
	}
	for _, p := range []string{"Artist/Alb/01.flac", "Artist/Alb/02.mp3"} {
		tr, err := tags.Read(filepath.Join(h.lib.Root(), p))
		if err != nil || tr.Album != "Album X" {
			t.Errorf("%s: album %q, %v", p, tr.Album, err)
		}
	}
	h.wantScreen("Artist — Album X")

	// track edit; Genre on 02 is untouched
	h.keys("right", "down", "e")
	h.wantScreen("Edit track Artist/Alb/02.mp3", "Title:")
	h.keys("ctrl+u")
	h.typeText("Second")
	h.keys("enter", "y")
	if tr, _ := tags.Read(filepath.Join(h.lib.Root(), "Artist/Alb/02.mp3")); tr.Title != "Second" || tr.Genre != "Rock" {
		t.Errorf("track edit: %+v", tr)
	}

	// invalid values are reported, the form stays open
	h.keys("e")
	for range 5 { // Title, Artist, Artists, Album, Album Artist → Date
		h.keys("down")
	}
	h.typeText("soon")
	h.keys("enter")
	if !h.app.status.err || !strings.Contains(h.status(), "date") {
		t.Errorf("status = %q", h.status())
	}
	h.keys("esc")
	h.wantScreen("Artist — Album X")
}

func TestLibraryFilter(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.addTrack("A/One/01.mp3", tags.Track{Title: "x", Album: "One", Artist: "A"})
		h.addTrack("B/Two/01.mp3", tags.Track{Title: "needle", Album: "Two", Artist: "B"})
	})
	h.keys("3", "/")
	h.typeText("needle")
	h.keys("enter")
	s := h.screen()
	if strings.Contains(s, "A — One") || !strings.Contains(s, "B — Two") {
		t.Errorf("filter by track title:\n%s", s)
	}
	h.keys("/", "esc")
	h.wantScreen("A — One", "B — Two")
}

func TestReviewApplyCandidate(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.addTrack("Unknown/rip/01.flac", tags.Track{Title: "Rock With You", Artist: "MJ", Album: "rip"})
		addReview(h, []string{"Unknown/rip/01.flac"}, []metadata.Match{
			{Score: 0.6, Assign: []int{0}, Release: metadata.Release{Provider: "musicbrainz", ID: "r1",
				URL: "https://musicbrainz.org/release/r1", Title: "Single Version", Artist: "Michael Jackson",
				Tracks: []metadata.Track{{Number: 1, Title: "Rock With You"}}}},
			{Score: 0.5, Assign: []int{1}, Release: metadata.Release{Provider: "musicbrainz", ID: "r2",
				URL: "https://musicbrainz.org/release/r2", Title: "Off the Wall", Artist: "Michael Jackson",
				Date: "1979-08-10", ReleaseType: "album",
				Tracks: []metadata.Track{{Number: 1, Title: "Don't Stop"}, {Number: 2, Title: "Rock With You", Duration: 220 * time.Second}}}},
		})
	})
	h.wantScreen("4 Review (1)")
	h.keys("4")
	h.wantScreen("Unknown/rip", "Your files", "Candidate 1 of 2", "Single Version")
	h.keys("right")
	h.wantScreen("Candidate 2 of 2", "Michael Jackson — Off the Wall", "1979-08-10 · album", "02 Rock With You 3:40")
	h.keys("enter")
	h.wantScreen("Preview:", "Album:        rip → Off the Wall")
	h.keys("y")
	if !strings.Contains(h.status(), "tagged 1 files") {
		t.Fatalf("status = %q", h.status())
	}
	tr, _ := tags.Read(filepath.Join(h.lib.Root(), "Unknown/rip/01.flac"))
	if tr.Album != "Off the Wall" || tr.TrackNumber != 2 || tr.Date != "1979-08-10" {
		t.Errorf("tags = %+v", tr)
	}
	h.wantScreen("Nothing to review")
	if strings.Contains(h.screen(), "Review (") {
		t.Error("review count still shown")
	}
}

func TestReviewSkip(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.addTrack("U/r/01.mp3", tags.Track{Title: "T"})
		addReview(h, []string{"U/r/01.mp3"}, nil)
	})
	h.keys("4", "s", "n")
	h.wantScreen("U/r")
	h.keys("s", "y")
	h.wantScreen("Nothing to review")
	if !strings.Contains(h.status(), "skipped") {
		t.Errorf("status = %q", h.status())
	}
}

func addReview(h *harness, paths []string, cands []metadata.Match) {
	h.t.Helper()
	p, _ := json.Marshal(paths)
	q, _ := json.Marshal(metadata.Query{Artist: "MJ", Album: "rip"})
	if cands == nil {
		cands = []metadata.Match{}
	}
	c, _ := json.Marshal(cands)
	if _, err := h.db.Exec("INSERT INTO review_items (path, paths, query, candidates, created_at) VALUES (?, ?, ?, ?, 0)",
		filepath.Dir(paths[0]), string(p), string(q), string(c)); err != nil {
		h.t.Fatal(err)
	}
}

func TestSettingsSwitchPreset(t *testing.T) {
	h := newHarness(t, nil)
	h.keys("esc", "5")
	h.wantScreen("Library", "* full", "mp3-player")
	h.keys("down", "enter")
	if !strings.Contains(h.status(), `active preset is now "mp3-player"`) {
		t.Fatalf("status = %q", h.status())
	}
	data, _ := os.ReadFile(h.cfgPath)
	if !strings.Contains(string(data), "active_preset: mp3-player") || !strings.Contains(string(data), "# test config") {
		t.Errorf("config file:\n%s", data)
	}
	h.wantScreen("* mp3-player")
	if h.app.s.preset().Name != "mp3-player" {
		t.Errorf("preset in use = %q", h.app.s.preset().Name)
	}
}

func TestScreensFitTheWindow(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.addTrack("A Very Long Artist Name Indeed/An Album With A Long Title/01.mp3",
			tags.Track{Title: "A track title that goes on and on and on", Album: "An Album With A Long Title", Artist: "A Very Long Artist Name Indeed"})
		addReview(h, []string{"A Very Long Artist Name Indeed/An Album With A Long Title/01.mp3"}, nil)
	})
	h.keys("/")
	h.typeText("o")
	h.keys("enter")
	for _, size := range [][2]int{{140, 40}, {80, 24}, {50, 12}} {
		h.send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for i := range h.app.screens {
			h.app.active = i
			lines := strings.Split(h.app.View().Content, "\n")
			if len(lines) != size[1] {
				t.Errorf("%dx%d %s: %d lines", size[0], size[1], h.app.screens[i].name(), len(lines))
			}
			for n, l := range lines {
				if w := ansi.StringWidth(l); w > size[0] {
					t.Errorf("%dx%d %s line %d: width %d", size[0], size[1], h.app.screens[i].name(), n, w)
				}
			}
		}
	}
}

func TestQuitWhenIdle(t *testing.T) {
	h := newHarness(t, nil)
	h.keys("esc", "2", "q")
	if !h.quit {
		t.Error("q did not quit")
	}
}
