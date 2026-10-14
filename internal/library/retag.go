package library

import (
	"context"
	"os"
	"path"

	"your-music-library/internal/config"
	"your-music-library/internal/tags"
)

// RetagItem is one file whose tags would change.
type RetagItem struct {
	Path    string
	Changes []tags.Change
	// SaveImages are album-folder images (e.g. "cover.jpg") written from
	// the embedded picture before it is shrunk or removed, so the full-size
	// image is never lost.
	SaveImages []string
}

// RetagPlan is the preview produced by PlanRetag.
type RetagPlan struct {
	Items     []RetagItem
	Unchanged int
	Failed    []FileError
}

// PlanRetag reads every indexed file and lists the changes that applying
// preset would make. It changes nothing.
func (l *Library) PlanRetag(ctx context.Context, preset config.Preset) (RetagPlan, error) {
	var plan RetagPlan
	es, err := l.Entries(ctx)
	if err != nil {
		return plan, err
	}
	saving := map[string]bool{} // folder image already planned
	for _, e := range es {
		if err := ctx.Err(); err != nil {
			return plan, err
		}
		cur, err := tags.Read(l.abs(e.Path))
		if err != nil {
			plan.Failed = append(plan.Failed, FileError{e.Path, err})
			continue
		}
		want, err := tags.ApplyPreset(cur, preset)
		if err != nil {
			plan.Failed = append(plan.Failed, FileError{e.Path, err})
			continue
		}
		changes := tags.Diff(cur, want)
		if c, ok := l.trackNumberFormatChange(e.Path, cur, want, changes); ok {
			changes = append(changes, c)
		}
		if len(changes) == 0 {
			plan.Unchanged++
			continue
		}
		item := RetagItem{Path: e.Path, Changes: changes}
		dir := path.Dir(e.Path)
		for _, pic := range []struct {
			old, new *tags.Picture
			base     string
		}{{cur.Cover, want.Cover, "cover"}, {cur.Back, want.Back, "back"}} {
			if pic.old == nil || (pic.new != nil && string(pic.new.Data) == string(pic.old.Data)) {
				continue
			}
			name := pic.base + imageExt(pic.old.MIME)
			if saving[path.Join(dir, pic.base)] || l.hasFolderImage(dir, pic.base) {
				continue
			}
			saving[path.Join(dir, pic.base)] = true
			item.SaveImages = append(item.SaveImages, name)
		}
		plan.Items = append(plan.Items, item)
	}
	return plan, nil
}

// trackNumberFormatChange reports a file whose track number is right but
// stored in another format ("1" or "3/12" instead of "01"). Diff cannot see
// this because Read parses both forms into the same numbers.
func (l *Library) trackNumberFormatChange(rel string, cur, want tags.Track, changes []tags.Change) (tags.Change, bool) {
	if want.TrackNumber == 0 || cur.TrackNumber != want.TrackNumber {
		return tags.Change{}, false
	}
	for _, c := range changes {
		if c.Field == config.FieldTrackNumber {
			return tags.Change{}, false
		}
	}
	abs := l.abs(rel)
	stored, err := tags.StoredTrackNumber(abs)
	if err != nil {
		return tags.Change{}, false
	}
	canonical, err := tags.CanonicalTrackNumber(abs, want)
	if err != nil || stored == canonical {
		return tags.Change{}, false
	}
	return tags.Change{Field: config.FieldTrackNumber, Old: stored, New: canonical}, true
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

// ApplyRetag writes the preset to every file in plan.
func (l *Library) ApplyRetag(ctx context.Context, plan RetagPlan, preset config.Preset) (ApplyReport, error) {
	var r ApplyReport
	for _, item := range plan.Items {
		if err := ctx.Err(); err != nil {
			return r, err
		}
		if err := l.retagOne(ctx, item, preset); err != nil {
			r.Failed = append(r.Failed, FileError{item.Path, err})
			continue
		}
		r.Changed++
	}
	return r, nil
}

func (l *Library) retagOne(ctx context.Context, item RetagItem, preset config.Preset) error {
	abs := l.abs(item.Path)
	cur, err := tags.Read(abs)
	if err != nil {
		return err
	}
	for _, name := range item.SaveImages {
		pic := cur.Cover
		if name[:4] == "back" {
			pic = cur.Back
		}
		dst := l.abs(path.Join(path.Dir(item.Path), name))
		if pic == nil || exists(dst) {
			continue
		}
		if err := os.WriteFile(dst, pic.Data, 0o644); err != nil {
			return err
		}
	}
	want, err := tags.ApplyPreset(cur, preset)
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
