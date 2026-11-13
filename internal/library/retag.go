package library

import (
	"bytes"
	"context"
	"os"
	"path"

	"your-music-library/internal/tags"
)

// RetagItem is one file whose tags would change.
type RetagItem struct {
	Path    string
	Changes []tags.Change
	// SaveImages are album-folder images ("cover.jpg") written from the
	// embedded front cover before it is shrunk, so the full-size image is
	// never lost.
	SaveImages []string
}

// RetagPlan is the preview produced by PlanRetag.
type RetagPlan struct {
	Items     []RetagItem
	Unchanged int
	Failed    []FileError
}

// PlanRetag reads every indexed file and lists the changes that bringing
// it to the fixed tag format (tags.Format) would make: totals and back
// covers removed, ASCII text, guests moved into titles, grouped singles
// restored, numbers padded. Tags ymlib doesn't manage are kept. It
// changes nothing.
func (l *Library) PlanRetag(ctx context.Context) (RetagPlan, error) {
	var plan RetagPlan
	es, err := l.Entries(ctx)
	if err != nil {
		return plan, err
	}
	saving := map[string]bool{} // album folders whose cover.jpg is already planned
	for _, e := range es {
		if err := ctx.Err(); err != nil {
			return plan, err
		}
		cur, err := tags.Read(l.abs(e.Path))
		if err != nil {
			plan.Failed = append(plan.Failed, FileError{e.Path, err})
			continue
		}
		want, err := tags.Format(cur)
		if err != nil {
			plan.Failed = append(plan.Failed, FileError{e.Path, err})
			continue
		}
		changes := tags.Diff(cur, want)
		changes = append(changes, l.numberFormatChanges(e.Path, want, changes)...)
		if len(changes) == 0 {
			plan.Unchanged++
			continue
		}
		item := RetagItem{Path: e.Path, Changes: changes}
		dir := path.Dir(e.Path)
		if shrunk := cur.Cover != nil && !bytes.Equal(cur.Cover.Data, want.Cover.Data); shrunk &&
			!saving[dir] && !l.hasFolderImage(dir, "cover") {
			saving[dir] = true
			item.SaveImages = append(item.SaveImages, "cover"+imageExt(cur.Cover.MIME))
		}
		plan.Items = append(plan.Items, item)
	}
	return plan, nil
}

// numberFormatChanges reports track and disc numbers that are right but
// stored in another form ("1" or "3/12" instead of "01"). Diff cannot see
// this because Read parses both forms into the same numbers.
func (l *Library) numberFormatChanges(rel string, want tags.Track, changes []tags.Change) []tags.Change {
	listed := map[tags.Field]bool{}
	for _, c := range changes {
		listed[c.Field] = true
	}
	track, disc, err := tags.StoredNumbers(l.abs(rel))
	if err != nil {
		return nil
	}
	var out []tags.Change
	for _, n := range []struct {
		field  tags.Field
		stored string
		want   int
	}{{tags.FieldTrackNumber, track, want.TrackNumber}, {tags.FieldDiscNumber, disc, want.DiscNumber}} {
		if n.want == 0 || listed[n.field] {
			continue
		}
		if padded := tags.PadNumber(n.want); n.stored != padded {
			out = append(out, tags.Change{Field: n.field, Old: n.stored, New: padded})
		}
	}
	return out
}

func imageExt(mime string) string {
	if mime == "image/png" {
		return ".png"
	}
	return ".jpg"
}

func (l *Library) hasFolderImage(dir, base string) bool {
	return exists(l.abs(path.Join(dir, base+".jpg"))) || exists(l.abs(path.Join(dir, base+".png")))
}

// ApplyRetag brings every file in plan to the fixed tag format.
func (l *Library) ApplyRetag(ctx context.Context, plan RetagPlan) (ApplyReport, error) {
	var r ApplyReport
	for _, item := range plan.Items {
		if err := ctx.Err(); err != nil {
			return r, err
		}
		if err := l.retagOne(ctx, item); err != nil {
			r.Failed = append(r.Failed, FileError{item.Path, err})
			continue
		}
		r.Changed++
	}
	return r, nil
}

func (l *Library) retagOne(ctx context.Context, item RetagItem) error {
	abs := l.abs(item.Path)
	cur, err := tags.Read(abs)
	if err != nil {
		return err
	}
	for _, name := range item.SaveImages {
		dst := l.abs(path.Join(path.Dir(item.Path), name))
		if cur.Cover == nil || exists(dst) {
			continue
		}
		if err := os.WriteFile(dst, cur.Cover.Data, 0o644); err != nil {
			return err
		}
	}
	want, err := tags.Format(cur)
	if err != nil {
		return err
	}
	if err := tags.Write(abs, want); err != nil {
		return err
	}
	return l.reindex(ctx, item.Path)
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
