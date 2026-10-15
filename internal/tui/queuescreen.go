package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"your-music-library/internal/queue"
	"your-music-library/internal/source"
)

// queueScreen shows download jobs and the runner's log.
type queueScreen struct {
	s      *shared
	jobs   []queue.Job
	cursor int
	err    error
}

func newQueueScreen(s *shared) *queueScreen { return &queueScreen{s: s} }

func (m *queueScreen) name() string { return "Queue" }

func (m *queueScreen) init() tea.Cmd { return nil } // App.Init loads the queue

func (m *queueScreen) capturing() bool { return false }

func (m *queueScreen) help() string {
	return "s start/resume · x stop · r retry job · R retry all · c clear done · C clear done+failed"
}

func (m *queueScreen) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case queueMsg:
		m.jobs, m.err = msg.jobs, msg.err
		m.cursor = max(0, min(m.cursor, len(m.jobs)-1))
	case refreshMsg:
		if msg.queue {
			return loadQueue(m.s)
		}
	case tea.KeyPressMsg:
		return m.key(msg.String())
	}
	return nil
}

func (m *queueScreen) key(key string) tea.Cmd {
	if c, ok := move(m.cursor, len(m.jobs), key, 10); ok {
		m.cursor = c
		return nil
	}
	ctx, q := m.s.ctx, m.s.d.Queue
	switch key {
	case "s":
		if m.s.dl.running {
			return status("downloads are already running")
		}
		return tea.Batch(status("downloads started"), m.s.startDownloads())
	case "x":
		if !m.s.dl.running {
			return status("downloads are not running")
		}
		m.s.stopDownloads()
		return status("stopping downloads… unfinished jobs resume with s")
	case "r", "R":
		var id int64
		if key == "r" {
			if m.cursor >= len(m.jobs) || m.jobs[m.cursor].Status != queue.Failed {
				return status("select a failed job, or press R to retry all")
			}
			id = m.jobs[m.cursor].ID
		}
		n, err := q.Retry(ctx, id)
		if err != nil {
			return errStatus(err)
		}
		if n == 0 {
			return status("no failed jobs")
		}
		return tea.Batch(status("%d jobs reset", n), loadQueue(m.s), m.s.startDownloads())
	case "c", "C":
		n, err := q.Clear(ctx, key == "C")
		if err != nil {
			return errStatus(err)
		}
		return tea.Batch(status("%d jobs removed", n), loadQueue(m.s))
	}
	return nil
}

func jobIcon(st queue.Status) string {
	switch st {
	case queue.Pending:
		return stDim.Render("·")
	case queue.Running:
		return stAccent.Render("↓")
	case queue.Done:
		return stOK.Render("✓")
	case queue.Failed:
		return stErr.Render("✗")
	}
	return " "
}

func (m *queueScreen) view(w, h int) string {
	c := m.s.counts
	state := "idle"
	if m.s.dl.running {
		state = stAccent.Render("running")
	}
	lines := []string{fmt.Sprintf("Downloads %s · %d waiting · %d running · %d failed", state, c.pending, c.running, c.failed)}
	if m.err != nil {
		lines = append(lines, stErr.Render(m.err.Error()))
	}
	logH := min(8, max(0, (h-len(lines))/3))
	listH := h - len(lines) - logH - 1
	if len(m.jobs) == 0 {
		lines = append(lines, "", "The queue is empty. Find music on the Search screen.")
	} else {
		lines = append(lines, listLines(len(m.jobs), m.cursor, w, listH, true, m.row)...)
	}
	if logH == 0 {
		return strings.Join(lines, "\n")
	}
	for len(lines) < h-logH-1 {
		lines = append(lines, "")
	}
	lines = append(lines, stDim.Render(strings.Repeat("─", w)))
	log := m.s.dl.log
	if len(log) > logH {
		log = log[len(log)-logH:]
	}
	return strings.Join(append(lines, log...), "\n")
}

func (m *queueScreen) row(i int) string {
	j := m.jobs[i]
	name := strings.TrimSpace(j.Item.Artist + " — " + j.Item.Title)
	if j.Item.Title == "" {
		name = stDim.Render("(not resolved yet)")
	}
	indent := ""
	if j.ParentID != 0 {
		indent = "  "
	}
	kind := ""
	if j.Kind == source.KindAlbum {
		kind = "album "
	}
	result := j.Note
	if j.File != "" && j.Note != "downloaded" {
		result = strings.TrimSpace(j.Note + ": " + j.File)
	}
	if j.LastError != "" {
		result = stErr.Render(fmt.Sprintf("attempt %d: %s", j.Attempts, j.LastError))
	}
	if result != "" {
		result = "  " + stDim.Render("→ ") + result
	}
	return fmt.Sprintf("%s%s %4d %s%s%s", indent, jobIcon(j.Status), j.ID, kind, name, result)
}
