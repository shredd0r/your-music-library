package tags

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// codec reads and writes one container format. Write replaces every tag
// this package manages with the values in t (empty values remove the tag)
// and keeps all other tags.
type codec interface {
	read(path string) (Track, error)
	write(path string, t Track) error
	// storedTrackNumber returns the track number tag exactly as stored.
	storedTrackNumber(path string) (string, error)
	// canonicalTrackNumber is how write stores t's track number.
	canonicalTrackNumber(t Track) string
}

var codecs = map[string]codec{
	".flac": flacCodec{},
	".mp3":  mp3Codec{},
}

// Supported reports whether path has a format this package can tag.
func Supported(path string) bool {
	_, ok := codecs[strings.ToLower(filepath.Ext(path))]
	return ok
}

// AudioExtensions are extensions treated as audio when scanning, including
// formats that cannot be tagged yet (reported as unsupported).
var AudioExtensions = map[string]bool{
	".flac": true,
	".mp3":  true,
	".m4a":  true,
	".mp4":  true,
	".aac":  true,
	".alac": true,
	".ogg":  true,
	".opus": true,
	".wav":  true,
	".aiff": true,
	".aif":  true,
	".wma":  true,
	".ape":  true,
	".wv":   true,
	".dsf":  true,
}

// IsAudio reports whether path looks like an audio file.
func IsAudio(path string) bool {
	return AudioExtensions[strings.ToLower(filepath.Ext(path))]
}

func codecFor(path string) (codec, error) {
	c, ok := codecs[strings.ToLower(filepath.Ext(path))]
	if !ok {
		return nil, fmt.Errorf("%s: unsupported format %q", path, filepath.Ext(path))
	}
	return c, nil
}

// Read returns the tags of the file at path.
func Read(path string) (Track, error) {
	c, err := codecFor(path)
	if err != nil {
		return Track{}, err
	}
	t, err := c.read(path)
	if err != nil {
		return Track{}, fmt.Errorf("reading tags of %s: %w", path, err)
	}
	return t, nil
}

// Write replaces the managed tags of the file at path with t.
func Write(path string, t Track) error {
	c, err := codecFor(path)
	if err != nil {
		return err
	}
	if err := c.write(path, t); err != nil {
		return fmt.Errorf("writing tags of %s: %w", path, err)
	}
	return nil
}

// StoredTrackNumber returns the file's track number tag as text, e.g. "3"
// or "3/12". Read parses that into numbers, so this is how callers notice
// files whose tag is not yet in the canonical zero-padded form.
func StoredTrackNumber(path string) (string, error) {
	c, err := codecFor(path)
	if err != nil {
		return "", err
	}
	return c.storedTrackNumber(path)
}

// CanonicalTrackNumber is the text Write would store for t's track number
// in the file at path: "01" (FLAC, total kept in TRACKTOTAL) or "01/11" (MP3).
func CanonicalTrackNumber(path string, t Track) (string, error) {
	c, err := codecFor(path)
	if err != nil {
		return "", err
	}
	return c.canonicalTrackNumber(t), nil
}

// rename is os.Rename; a variable seam kept for tests.
var rename = os.Rename

// coverFallbackIndex returns the index of the picture that Read reports
// as the cover when no front cover exists: the first picture of type
// "Other", which some tools (ffmpeg, rippers) use for album art. Write
// replaces that picture; -1 means there is none.
func coverFallbackIndex[T comparable](types []T, front, other T) int {
	if slices.Contains(types, front) {
		return -1
	}

	for i, t := range types {
		if t == other {
			return i
		}
	}

	return -1
}
