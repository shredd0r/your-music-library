package download

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	flac "github.com/go-flac/go-flac/v2"

	"your-music-library/internal/cleanup"
	"your-music-library/internal/tags"
)

// permanentError marks failures that a retry cannot fix.
type permanentError struct{ error }

func (p permanentError) Unwrap() error { return p.error }

// Permanent wraps err so the job fails without retries.
func Permanent(err error) error { return permanentError{err} }

// IsPermanent reports whether err was wrapped by Permanent.
func IsPermanent(err error) bool {
	var p permanentError
	return errors.As(err, &p)
}

// Verifier checks a downloaded file; expected is the length the source
// announced (0 if unknown).
type Verifier func(ctx context.Context, path string, expected time.Duration) error

// Verify is the default Verifier: the format must be supported, tags must
// parse, the audio must decode (with ffmpeg, when installed) and the
// length must match the announced duration (truncated downloads).
func Verify(ctx context.Context, path string, expected time.Duration) error {
	if !tags.Supported(path) {
		return Permanent(fmt.Errorf("unsupported format %q (ymlib handles FLAC and MP3)", strings.ToLower(extOf(path))))
	}
	if _, err := tags.Read(path); err != nil {
		return fmt.Errorf("not a valid audio file: %w", err)
	}
	actual, err := measure(ctx, path)
	if err != nil {
		return err
	}
	if expected > 0 && actual > 0 {
		tolerance := max(3*time.Second, expected/50)
		if d := actual - expected; d > tolerance || -d > tolerance {
			return fmt.Errorf("length %s, expected %s (incomplete download?)", actual.Round(time.Second), expected)
		}
	}
	return nil
}

func extOf(path string) string {
	if i := strings.LastIndexByte(path, '.'); i >= 0 {
		return path[i:]
	}
	return ""
}

// measure decodes the file with ffmpeg if available and returns its
// length. Without ffmpeg only FLAC length is known (from STREAMINFO).
func measure(ctx context.Context, path string) (_ time.Duration, err error) {
	if ffmpeg, err := exec.LookPath("ffmpeg"); err == nil {
		var stderr bytes.Buffer
		cmd := exec.CommandContext(ctx, ffmpeg, "-v", "error", "-nostdin", "-i", path, "-f", "null", "-")
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil || strings.TrimSpace(stderr.String()) != "" {
			return 0, fmt.Errorf("audio does not decode: %s", firstLine(stderr.String(), err))
		}
		if ffprobe, err := exec.LookPath("ffprobe"); err == nil {
			out, err := exec.CommandContext(ctx, ffprobe, "-v", "error", "-show_entries", "format=duration",
				"-of", "default=noprint_wrappers=1:nokey=1", path).Output()
			if err == nil {
				if secs, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64); err == nil {
					return time.Duration(secs * float64(time.Second)), nil
				}
			}
		}
		return 0, nil
	}
	if strings.EqualFold(extOf(path), ".flac") {
		f, err := flac.ParseFile(path)
		if err != nil {
			return 0, err
		}
		defer cleanup.Close(&err, f)
		si, err := f.GetStreamInfo()
		if err != nil || si.SampleRate == 0 {
			return 0, err
		}
		return time.Duration(float64(si.SampleCount) / float64(si.SampleRate) * float64(time.Second)), nil
	}
	return 0, nil
}

func firstLine(s string, err error) string {
	if s = strings.TrimSpace(s); s != "" {
		return strings.SplitN(s, "\n", 2)[0]
	}
	if err != nil {
		return err.Error()
	}
	return "unknown error"
}
