package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"time"

	"your-music-library/internal/config"
	"your-music-library/internal/metadata"
)

// Review is an open Needs Review item.
type Review struct {
	ID         int64
	Folder     string
	Paths      []string
	Query      metadata.Query
	Candidates []metadata.Match
	Created    time.Time
}

// addReview stores d, replacing open reviews that cover the same files.
func (l *Library) addReview(ctx context.Context, d ReviewDraft) error {
	paths, err := json.Marshal(d.Paths)
	if err != nil {
		return err
	}
	query, err := json.Marshal(d.Query)
	if err != nil {
		return err
	}
	cands, err := json.Marshal(d.Candidates)
	if err != nil {
		return err
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM review_items WHERE status = 'open' AND paths = ?", string(paths)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO review_items (path, paths, query, candidates, created_at) VALUES (?, ?, ?, ?, ?)",
		d.Folder, string(paths), string(query), string(cands), l.now().Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

// Reviews returns open review items, oldest first.
func (l *Library) Reviews(ctx context.Context) ([]Review, error) {
	rows, err := l.db.QueryContext(ctx,
		"SELECT id, path, paths, query, candidates, created_at FROM review_items WHERE status = 'open' ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Review
	for rows.Next() {
		r, err := scanReview(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ErrNoReview is returned for an unknown or closed review ID.
var ErrNoReview = errors.New("no open review with this id")

func (l *Library) review(ctx context.Context, id int64) (Review, error) {
	row := l.db.QueryRowContext(ctx,
		"SELECT id, path, paths, query, candidates, created_at FROM review_items WHERE id = ? AND status = 'open'", id)
	r, err := scanReview(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Review{}, ErrNoReview
	}
	return r, err
}

func scanReview(s scanner) (Review, error) {
	var r Review
	var paths, query, cands string
	var created int64
	if err := s.Scan(&r.ID, &r.Folder, &paths, &query, &cands, &created); err != nil {
		return Review{}, err
	}
	r.Created = time.Unix(created, 0)
	if err := json.Unmarshal([]byte(paths), &r.Paths); err != nil {
		return Review{}, err
	}
	if err := json.Unmarshal([]byte(query), &r.Query); err != nil {
		return Review{}, err
	}
	if err := json.Unmarshal([]byte(cands), &r.Candidates); err != nil {
		return Review{}, err
	}
	return r, nil
}

// PlanResolve previews applying candidate n (1-based) of review id. If
// refiner is not nil it is asked for a full release date first.
func (l *Library) PlanResolve(ctx context.Context, id int64, n int, refiner *metadata.Identifier) (AlbumMatch, error) {
	r, err := l.review(ctx, id)
	if err != nil {
		return AlbumMatch{}, err
	}
	if n < 1 || n > len(r.Candidates) {
		return AlbumMatch{}, fmt.Errorf("review %d has %d candidates", id, len(r.Candidates))
	}
	es, err := l.Entries(ctx)
	if err != nil {
		return AlbumMatch{}, err
	}
	byPath := map[string]Entry{}
	for _, e := range es {
		byPath[e.Path] = e
	}
	var entries []Entry
	for _, p := range r.Paths {
		e, ok := byPath[p]
		if !ok {
			return AlbumMatch{}, fmt.Errorf("%s is no longer in the library (moved or deleted); run identify again", p)
		}
		entries = append(entries, e)
	}
	m := r.Candidates[n-1]
	if refiner != nil {
		refiner.Refine(ctx, &m.Release)
	}
	return buildAlbumMatch(r.Folder, entries, m), nil
}

// ApplyResolve writes the chosen candidate and closes the review.
func (l *Library) ApplyResolve(ctx context.Context, id int64, am AlbumMatch, preset config.Preset, images ImageSource) (IdentifyReport, error) {
	r, err := l.ApplyIdentify(ctx, IdentifyPlan{Matches: []AlbumMatch{am}}, preset, images)
	if err != nil {
		return r, err
	}
	if len(r.Failed) == 0 {
		_, err = l.db.ExecContext(ctx, "UPDATE review_items SET status = 'resolved' WHERE id = ?", id)
	}
	return r, err
}

// SkipReview closes a review without changing files.
func (l *Library) SkipReview(ctx context.Context, id int64) error {
	res, err := l.db.ExecContext(ctx, "UPDATE review_items SET status = 'skipped' WHERE id = ? AND status = 'open'", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoReview
	}
	return nil
}

// updateOpenReviews rewrites or closes open reviews. fn gets each review's
// paths and returns the new paths and whether to close it.
func (l *Library) updateOpenReviews(ctx context.Context, fn func(paths []string) ([]string, bool)) error {
	reviews, err := l.Reviews(ctx)
	if err != nil {
		return err
	}
	for _, r := range reviews {
		newPaths, closeIt := fn(r.Paths)
		if closeIt {
			if _, err := l.db.ExecContext(ctx, "UPDATE review_items SET status = 'resolved' WHERE id = ?", r.ID); err != nil {
				return err
			}
			continue
		}
		if newPaths == nil {
			continue
		}
		data, err := json.Marshal(newPaths)
		if err != nil {
			return err
		}
		if _, err := l.db.ExecContext(ctx, "UPDATE review_items SET paths = ?, path = ? WHERE id = ?",
			string(data), path.Dir(newPaths[0]), r.ID); err != nil {
			return err
		}
	}
	return nil
}

// followMoves points open reviews at files' new paths after organize.
func (l *Library) followMoves(ctx context.Context, moved map[string]string) error {
	if len(moved) == 0 {
		return nil
	}
	return l.updateOpenReviews(ctx, func(paths []string) ([]string, bool) {
		changed := false
		out := make([]string, len(paths))
		for i, p := range paths {
			out[i] = p
			if to, ok := moved[p]; ok {
				out[i], changed = to, true
			}
		}
		if !changed {
			return nil, false
		}
		return out, false
	})
}

// closeReviewsFor resolves open reviews covering any of the tagged files.
func (l *Library) closeReviewsFor(ctx context.Context, tagged map[string]bool) error {
	if len(tagged) == 0 {
		return nil
	}
	return l.updateOpenReviews(ctx, func(paths []string) ([]string, bool) {
		for _, p := range paths {
			if tagged[p] {
				return nil, true
			}
		}
		return nil, false
	})
}
