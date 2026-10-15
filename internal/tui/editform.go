package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"your-music-library/internal/config"
	"your-music-library/internal/library"
)

type (
	editPlanMsg struct {
		plan library.EditPlan
		err  error
	}
	editDoneMsg struct {
		rep library.ApplyReport
		err error
	}
)

// editForm edits text tags of one track or a whole album: values are
// edited, then the changes are previewed and applied on confirmation.
type editForm struct {
	s      *shared
	title  string
	paths  []string
	fields []config.Field
	inputs []textinput.Model
	orig   []string
	focus  int
	plan   *library.EditPlan
	busy   bool
	done   bool // closed; the screen drops the form
}

// newEditForm prefills fields with the value shared by all tracks; mixed
// values start empty and are kept unless something is typed.
func newEditForm(s *shared, title string, tracks []library.Entry, fields []config.Field) (*editForm, tea.Cmd) {
	f := &editForm{s: s, title: title}
	for _, e := range tracks {
		f.paths = append(f.paths, e.Path)
	}
	for _, fld := range fields {
		val, mixed := library.FieldValue(tracks[0].Track, fld), false
		for _, e := range tracks[1:] {
			if library.FieldValue(e.Track, fld) != val {
				val, mixed = "", true
				break
			}
		}
		in := newInput(fmt.Sprintf("%-13s ", fld+":"))
		in.SetValue(val)
		if mixed {
			in.Placeholder = "(different values; leave empty to keep)"
		}
		f.fields = append(f.fields, fld)
		f.inputs = append(f.inputs, in)
		f.orig = append(f.orig, val)
	}
	return f, f.inputs[0].Focus()
}

func (f *editForm) help() string {
	if f.plan != nil {
		return "y write tags · n back to editing · esc cancel"
	}
	return "↑↓/tab field · enter preview changes · esc cancel"
}

func (f *editForm) edit() library.TagEdit {
	e := library.TagEdit{}
	for i, in := range f.inputs {
		if v := in.Value(); v != f.orig[i] {
			e[f.fields[i]] = v
		}
	}
	return e
}

func (f *editForm) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case editPlanMsg:
		f.busy = false
		if msg.err != nil {
			return errStatus(msg.err)
		}
		if len(msg.plan.Items) == 0 {
			return status("nothing to change")
		}
		f.plan = &msg.plan
	case editDoneMsg:
		f.busy, f.done = false, true
		if msg.err != nil {
			return tea.Batch(errStatus(msg.err), refresh(false, true, false))
		}
		text := fmt.Sprintf("retagged %d files", msg.rep.Changed)
		if n := len(msg.rep.Failed); n > 0 {
			text += fmt.Sprintf(", %d failed: %v", n, msg.rep.Failed[0].Err)
		} else {
			text += "; run `ymlib organize` to rename files from the new tags"
		}
		return tea.Batch(func() tea.Msg { return statusMsg{text: text, err: len(msg.rep.Failed) > 0} },
			refresh(false, true, false))
	case tea.KeyPressMsg:
		if f.busy {
			return nil
		}
		if f.plan != nil {
			return f.previewKey(msg.String())
		}
		return f.formKey(msg)
	}
	return nil
}

func (f *editForm) formKey(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "esc":
		f.done = true
		return status("edit cancelled")
	case "up", "shift+tab":
		return f.setFocus(f.focus - 1)
	case "down", "tab":
		return f.setFocus(f.focus + 1)
	case "enter":
		e := f.edit()
		if len(e) == 0 {
			return status("nothing changed")
		}
		f.busy = true
		s, paths := f.s, f.paths
		return func() tea.Msg {
			p, err := s.d.Lib.PlanEdit(s.ctx, paths, e)
			return editPlanMsg{plan: p, err: err}
		}
	}
	var cmd tea.Cmd
	f.inputs[f.focus], cmd = f.inputs[f.focus].Update(k)
	return cmd
}

func (f *editForm) setFocus(i int) tea.Cmd {
	i = (i + len(f.inputs)) % len(f.inputs)
	f.inputs[f.focus].Blur()
	f.focus = i
	return f.inputs[i].Focus()
}

func (f *editForm) previewKey(key string) tea.Cmd {
	switch key {
	case "y":
		f.busy = true
		s, plan := f.s, *f.plan
		return func() tea.Msg {
			r, err := s.d.Lib.ApplyEdit(s.ctx, plan)
			return editDoneMsg{rep: r, err: err}
		}
	case "n":
		f.plan = nil
	case "esc":
		f.done = true
		return status("edit cancelled")
	}
	return nil
}

func (f *editForm) view(w, h int) string {
	lines := []string{stBold.Render(f.title), ""}
	if f.plan == nil {
		for _, in := range f.inputs {
			lines = append(lines, in.View())
		}
		lines = append(lines, "", stDim.Render("Only fields of the active preset are shown. Empty removes the tag."))
		return strings.Join(lines, "\n")
	}
	lines[0] = stBold.Render(f.title + ": preview")
	for _, it := range f.plan.Items {
		lines = append(lines, it.Path)
		for _, c := range it.Changes {
			lines = append(lines, "    "+changeLine(c.Field, c.Old, c.New))
		}
	}
	if f.plan.Unchanged > 0 {
		lines = append(lines, stDim.Render(fmt.Sprintf("%d files already have these values", f.plan.Unchanged)))
	}
	if len(lines) > h {
		lines = append(lines[:h-1], stDim.Render(fmt.Sprintf("… %d more lines", len(lines)-h+1)))
	}
	return strings.Join(lines, "\n")
}

func changeLine(f config.Field, old, new string) string {
	if old == "" {
		old = "(none)"
	}
	if new == "" {
		new = "(removed)"
	}
	return fmt.Sprintf("%-13s %s → %s", f+":", stDim.Render(old), new)
}
