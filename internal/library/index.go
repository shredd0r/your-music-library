package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"your-music-library/internal/cleanup"
	"your-music-library/internal/tags"
)

// Entry is one indexed file: its tags (without picture data) plus file facts.
type Entry struct {
	ID       int64
	Path     string // relative to the library root, '/'-separated
	Size     int64
	MtimeNs  int64
	HasCover bool
	tags.Track
}

const entryColumns = `id, path, size, mtime_ns, title, album, artist, artists, album_artist,
	date, track_number, track_total, disc_number, disc_total, genre, has_cover,
	release_type, original_album, original_track,
	source, source_track_id, isrc, mb_recording_id, mb_release_id, discogs_release_id`

type scanner interface{ Scan(dest ...any) error }

func scanEntry(s scanner) (Entry, error) {
	var e Entry
	var artists string
	err := s.Scan(&e.ID, &e.Path, &e.Size, &e.MtimeNs, &e.Title, &e.Album, &e.Artist, &artists,
		&e.AlbumArtist, &e.Date, &e.TrackNumber, &e.TrackTotal, &e.DiscNumber, &e.DiscTotal,
		&e.Genre, &e.HasCover, &e.ReleaseType, &e.OriginalAlbum, &e.OriginalTrack,
		&e.IDs.Source, &e.IDs.SourceTrackID, &e.IDs.ISRC, &e.IDs.MBRecordingID,
		&e.IDs.MBReleaseID, &e.IDs.DiscogsReleaseID)
	if err != nil {
		return Entry{}, err
	}
	if err := json.Unmarshal([]byte(artists), &e.Artists); err != nil {
		return Entry{}, err
	}
	return e, nil
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// upsert inserts or replaces the row for e.Path.
func upsert(ctx context.Context, db execer, e Entry, now time.Time) error {
	artists := e.Artists
	if artists == nil {
		artists = []string{}
	}
	aj, err := json.Marshal(artists)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO tracks (path, size, mtime_ns, title, album, artist, artists, album_artist,
			date, track_number, track_total, disc_number, disc_total, genre, has_cover,
			release_type, original_album, original_track,
			source, source_track_id, isrc, mb_recording_id, mb_release_id, discogs_release_id, indexed_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (path) DO UPDATE SET
			size=excluded.size, mtime_ns=excluded.mtime_ns, title=excluded.title,
			album=excluded.album, artist=excluded.artist, artists=excluded.artists,
			album_artist=excluded.album_artist, date=excluded.date,
			track_number=excluded.track_number, track_total=excluded.track_total,
			disc_number=excluded.disc_number, disc_total=excluded.disc_total,
			genre=excluded.genre, has_cover=excluded.has_cover,
			release_type=excluded.release_type, original_album=excluded.original_album,
			original_track=excluded.original_track, source=excluded.source,
			source_track_id=excluded.source_track_id, isrc=excluded.isrc,
			mb_recording_id=excluded.mb_recording_id, mb_release_id=excluded.mb_release_id,
			discogs_release_id=excluded.discogs_release_id, indexed_at=excluded.indexed_at`,
		e.Path, e.Size, e.MtimeNs, e.Title, e.Album, e.Artist, string(aj), e.AlbumArtist,
		e.Date, e.TrackNumber, e.TrackTotal, e.DiscNumber, e.DiscTotal, e.Genre, e.HasCover,
		e.ReleaseType, e.OriginalAlbum, e.OriginalTrack,
		e.IDs.Source, e.IDs.SourceTrackID, e.IDs.ISRC, e.IDs.MBRecordingID, e.IDs.MBReleaseID,
		e.IDs.DiscogsReleaseID, now.Unix())
	return err
}

// Entries returns every indexed file ordered by path.
func (l *Library) Entries(ctx context.Context) (_ []Entry, err error) {
	rows, err := l.db.QueryContext(ctx, "SELECT "+entryColumns+" FROM tracks ORDER BY path")
	if err != nil {
		return nil, err
	}
	defer cleanup.Close(&err, rows)
	var out []Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
