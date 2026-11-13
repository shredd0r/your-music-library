package tui

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"your-music-library/internal/library"
)

type (
	// taskPlanMsg carries a planned library task, ready to preview.
	taskPlanMsg struct {
		task *libTask
		err  error
	}
	// taskDoneMsg reports an applied task.
	taskDoneMsg struct {
		text   string
		failed bool
		err    error
		// reviews: the task may have added items to the Review screen.
		reviews bool
	}
)

// taskAction is a key that applies a task's plan.
type taskAction struct {
	key, label string
	run        func() tea.Msg // returns taskDoneMsg
}

// libTask is a library-wide operation (tidy, identify, import): its plan
// is previewed, and a key applies it.
type libTask struct {
	title   string
	lines   []string
	top     int
	actions []taskAction
	busy    bool
	done    bool // closed; the screen drops it
}

func (t *libTask) help() string {
	if t.busy {
		return "working…"
	}
	var parts []string
	for _, a := range t.actions {
		parts = append(parts, a.key+" "+a.label)
	}
	return strings.Join(append(parts, "↑↓ scroll", "esc cancel"), " · ")
}

func (t *libTask) update(msg tea.Msg, page int) tea.Cmd {
	switch msg := msg.(type) {
	case taskDoneMsg:
		t.busy, t.done = false, true
		if msg.err != nil {
			return tea.Batch(errStatus(msg.err), refresh(true, true, true))
		}
		return tea.Batch(func() tea.Msg { return statusMsg{text: msg.text, err: msg.failed} },
			refresh(false, true, msg.reviews))
	case tea.KeyPressMsg:
		if t.busy {
			return nil
		}
		key := msg.String()
		if c, ok := move(t.top, len(t.lines), key, page); ok {
			t.top = c
			return nil
		}
		if key == "esc" {
			t.done = true
			return status("cancelled; nothing changed")
		}
		for _, a := range t.actions {
			if key == a.key || (a.key == "enter" && key == "y") {
				t.busy = true
				return tea.Batch(status("%s…", a.label), a.run)
			}
		}
	}
	return nil
}

func (t *libTask) view(w, h int) string {
	head := []string{stBold.Render(t.title), ""}
	room := max(1, h-len(head))
	top := max(0, min(t.top, len(t.lines)-room))
	end := min(len(t.lines), top+room)
	shown := slices.Clone(t.lines[top:end])
	if end < len(t.lines) && len(shown) > 0 {
		shown[len(shown)-1] = stDim.Render(fmt.Sprintf("… %d more lines (↓ scrolls)", len(t.lines)-end+1))
	}
	return strings.Join(append(head, shown...), "\n")
}

// Planning commands. Each returns a taskPlanMsg.

func planTidy(s *shared) tea.Cmd {
	return func() tea.Msg {
		if _, err := s.d.Lib.Scan(s.ctx); err != nil {
			return taskPlanMsg{err: err}
		}
		p, err := s.d.Lib.PlanTidy(s.ctx)
		if err != nil {
			return taskPlanMsg{err: err}
		}
		return taskPlanMsg{task: tidyTask(s, p)}
	}
}

func tidyTask(s *shared, p library.TidyPlan) *libTask {
	t := &libTask{title: "Tidy library: preview"}
	add := func(l ...string) { t.lines = append(t.lines, l...) }
	if len(p.Playlists) > 0 {
		add(stWarn.Render("Playlists could be broken: "+strings.Join(p.Playlists, ", ")),
			stDim.Render("  they list files that move; fix their paths afterwards"), "")
	}
	add(stBold.Render(fmt.Sprintf("Tag changes (%d files)", len(p.Retag.Items))))
	for _, it := range p.Retag.Items {
		add(it.Path)
		for _, c := range it.Changes {
			add("    " + changeLine(c.Field, c.Old, c.New))
		}
		for _, img := range it.SaveImages {
			add("    " + stDim.Render("+ save full-size "+img))
		}
	}
	for _, f := range p.Retag.Failed {
		add(stErr.Render(fmt.Sprintf("! %s: %v", f.Path, f.Err)))
	}
	add("", stBold.Render(fmt.Sprintf("File moves (%d files, %d folder images)", len(p.Organize.Moves), len(p.Organize.Images))))
	for _, m := range append(p.Organize.Moves, p.Organize.Images...) {
		add(m.From, "    → "+m.To)
	}
	for _, c := range p.Organize.Conflicts {
		add(stWarn.Render(fmt.Sprintf("! %s → %s: %s (skipped)", c.From, c.To, c.Reason)))
	}
	if len(p.Retag.Items)+len(p.Organize.Moves)+len(p.Organize.Images) == 0 {
		add("", "The library is already tidy.")
		return t
	}
	t.actions = []taskAction{{key: "enter", label: "tidy", run: func() tea.Msg {
		r, err := s.d.Lib.ApplyTidy(s.ctx, p)
		failed := append(r.Retag.Failed, r.Organize.Failed...)
		text := fmt.Sprintf("tidied: %d files retagged, %d moved", r.Retag.Changed, r.Organize.Changed)
		if len(failed) > 0 {
			text += fmt.Sprintf(", %d failed: %s: %v", len(failed), failed[0].Path, failed[0].Err)
		}
		return taskDoneMsg{text: text, failed: len(failed) > 0, err: err}
	}}}
	return t
}

// planIdentify looks up the albums under prefixes (all albums when nil).
// Files that already have a release ID are skipped.
func planIdentify(s *shared, prefixes []string, what string) tea.Cmd {
	return func() tea.Msg {
		if s.d.Identifier == nil {
			return taskPlanMsg{err: fmt.Errorf("no metadata provider is available (see the warning at start)")}
		}
		p, err := s.d.Lib.PlanIdentify(s.ctx, s.d.Identifier, library.IdentifyOptions{Prefixes: prefixes})
		if err != nil {
			return taskPlanMsg{err: err}
		}
		return taskPlanMsg{task: identifyTask(s, p, what)}
	}
}

func identifyTask(s *shared, p library.IdentifyPlan, what string) *libTask {
	t := &libTask{title: "Identify " + what + ": preview"}
	add := func(l ...string) { t.lines = append(t.lines, l...) }
	for _, m := range p.Matches {
		add(stOK.Render("✓ ")+m.Folder, fmt.Sprintf("  → %s — %s (%s)  score %.2f, %s",
			m.Release.Artist, m.Release.Title, orDash(m.Release.Date), m.Score, m.Release.Provider))
		for _, it := range m.Items {
			add("  " + it.Path)
			for _, c := range it.Changes {
				add("      " + changeLine(c.Field, c.Old, c.New))
			}
		}
		for _, u := range m.Unmatched {
			add(stWarn.Render("  ? " + u + ": no track on this release; left as is"))
		}
	}
	for _, d := range p.Reviews {
		add(stWarn.Render("? "+d.Folder) + stDim.Render(": no confident match; goes to Review"))
	}
	for _, u := range p.Untitled {
		add(stDim.Render("- " + u + ": no title, skipped"))
	}
	for _, f := range p.Failed {
		add(stErr.Render(fmt.Sprintf("! %s: %v", f.Path, f.Err)))
	}
	if p.Skipped > 0 {
		add(stDim.Render(fmt.Sprintf("%d files already have a release ID", p.Skipped)))
	}
	if len(p.Matches)+len(p.Reviews) == 0 {
		add("", "Nothing to identify.")
		return t
	}
	t.actions = []taskAction{{key: "enter", label: "write tags", run: func() tea.Msg {
		r, err := s.d.Lib.ApplyIdentify(s.ctx, p, s.d.Images)
		text := fmt.Sprintf("identified: %d files tagged, %d albums to review", r.Changed, r.Reviews)
		if len(r.Failed) > 0 {
			text += fmt.Sprintf(", %d failed: %v", len(r.Failed), r.Failed[0].Err)
		} else if len(r.Warnings) > 0 {
			text += "; " + r.Warnings[0]
		}
		return taskDoneMsg{text: text, failed: len(r.Failed) > 0, err: err, reviews: r.Reviews > 0}
	}}}
	return t
}

// planImport plans importing the folder dir (outside the library).
func planImport(s *shared, dir string) tea.Cmd {
	return func() tea.Msg {
		if _, err := s.d.Lib.Scan(s.ctx); err != nil {
			return taskPlanMsg{err: err}
		}
		p, err := s.d.Lib.PlanImport(s.ctx, dir, s.d.Identifier, library.IngestOptions{})
		if err != nil {
			return taskPlanMsg{err: err}
		}
		return taskPlanMsg{task: importTask(s, p, dir)}
	}
}

func importTask(s *shared, p library.IngestPlan, dir string) *libTask {
	t := &libTask{title: "Import " + dir + ": preview"}
	add := func(l ...string) { t.lines = append(t.lines, l...) }
	for _, m := range p.Matches {
		add(stOK.Render("✓ ")+m.Folder, fmt.Sprintf("  → %s — %s (%s)  score %.2f, %s",
			m.Release.Artist, m.Release.Title, orDash(m.Release.Date), m.Score, m.Release.Provider))
	}
	for _, d := range p.Reviews {
		add(stWarn.Render("? "+d.Folder) + stDim.Render(": no confident match; imported with its own tags and listed in Review"))
	}
	add("", stBold.Render(fmt.Sprintf("Files (%d to import)", len(p.Items))))
	for _, it := range p.Items {
		note := ""
		if it.Dup == library.OtherEdition {
			note = stDim.Render("  (≈ other edition in library: " + it.DupPath + ")")
		}
		add(it.Display, "    → "+it.Target+note)
	}
	for _, it := range p.Duplicates {
		add(stDim.Render("= " + it.Display + ": already in library (" + it.DupPath + ")"))
	}
	for _, c := range p.Conflicts {
		add(stWarn.Render(fmt.Sprintf("! %s → %s: %s (skipped)", c.From, c.To, c.Reason)))
	}
	for _, u := range p.Unsupported {
		add(stDim.Render("- " + u + ": unsupported format"))
	}
	for _, f := range p.Failed {
		add(stErr.Render(fmt.Sprintf("! %s: %v", f.Path, f.Err)))
	}
	if len(p.Items) == 0 {
		add("", "Nothing to import.")
		return t
	}
	apply := func(move bool) func() tea.Msg {
		return func() tea.Msg {
			r, err := s.d.Lib.ApplyIngest(s.ctx, p, s.d.Images, library.IngestOptions{Base: dir, Move: move})
			text := fmt.Sprintf("imported %d files, %d albums to review", len(r.Imported), r.Reviews)
			if len(r.Failed) > 0 {
				text += fmt.Sprintf(", %d failed: %v", len(r.Failed), r.Failed[0].Err)
			} else if len(r.Warnings) > 0 {
				text += "; " + r.Warnings[0]
			}
			return taskDoneMsg{text: text, failed: len(r.Failed) > 0, err: err, reviews: r.Reviews > 0}
		}
	}
	t.actions = []taskAction{
		{key: "enter", label: "import (copy)", run: apply(false)},
		{key: "m", label: "import and delete originals", run: apply(true)},
	}
	return t
}
