# ymlib: your music library

A command-line tool for keeping a local music collection clean: it indexes your files, looks up correct metadata on **Discogs** and **MusicBrainz**, writes tags in the format you choose, downloads covers, and sorts files into a consistent `Artist/Album/01 - Title.flac` layout.

> **Status: `0.1.0-dev`, Stage 1 in progress.** Tagging, organizing and metadata lookup work. Downloading from sources, the terminal UI, playlists and device sync are planned. See [MILESTONES.md](MILESTONES.md).

## What it does today

| Feature | Command |
|---|---|
| Index your music folder (reads tags only, changes nothing) | `ymlib scan` |
| Identify albums on Discogs/MusicBrainz and write tags and covers | `ymlib identify` |
| Choose a release by hand when a match isn't certain | `ymlib review` |
| Apply a metadata preset, e.g. slim tags and small covers for MP3 players | `ymlib retag` |
| Rename and move files according to a path template | `ymlib organize` |
| Group singles into one `<Artist> Singles` album, or split them back | `ymlib singles` |

**Nothing changes without asking.** Every command that modifies files shows a preview first and acts only with `--apply`. Files are never overwritten. Unknown tags (ReplayGain, comments, other pictures) are kept.

Supported formats: **FLAC** and **MP3** (ID3v2.4). Other audio files (M4A, OGG, OPUS, …) are listed by `scan` as unsupported and left alone.

## Requirements

- macOS or Linux
- Go 1.26+ to build
- Optional: a [Discogs personal access token](https://www.discogs.com/settings/developers), used as the primary metadata source. Without one, MusicBrainz is used on its own.

## Build

```sh
make build          # → bin/ymlib
./bin/ymlib --help
```

## Quick start

```sh
# 1. Create the config and point it at your music
ymlib config init                     # writes ~/.config/ymlib/config.yaml
$EDITOR ~/.config/ymlib/config.yaml   # set library_path

# 2. (Optional) store your Discogs token in the OS keychain
ymlib auth discogs

# 3. Index the library
ymlib scan

# 4. Look up metadata: preview, then apply
ymlib identify
ymlib identify --apply

# 5. Pick releases for albums that weren't matched confidently
ymlib review
ymlib review apply 1 2 --apply        # review #1, candidate 2

# 6. Rename files from their new tags
ymlib organize
ymlib organize --apply
```

## Commands

### `scan`
Walks `library_path`, reads tags and updates the index (SQLite). Files whose size and modification time haven't changed aren't read again. Hidden files and macOS `._*` files are skipped. `organize`, `retag`, `singles` and `identify` scan automatically first.

### `identify [folder...] [--force] [--apply]`
Groups files by album and looks each album up. It tries Discogs first, then MusicBrainz if Discogs fails, finds nothing, or finds nothing confident.

- **Whole-record matching:** every tag of a file comes from a single release, never a mix of sources.
- Matching ignores case, accents, typographic quotes and edition suffixes like "(Remastered 2009)". Compilations are scored down so a single isn't mistaken for track 7 of a "Hits of 1987" album.
- Matches scoring at least `metadata.min_score` (default 0.85) are applied. Weaker ones go to `ymlib review`.
- Dates are written as `yyyy-mm-dd` using the original release date. When a source only knows the year, the app looks further (the edition's date on MusicBrainz, the main edition on Discogs). A day is never invented.
- Covers are downloaded once per album. The full-size image is saved as `cover.jpg` (and `back.jpg`) in the album folder, and the embedded copy follows your preset.
- Files that already carry a release ID are skipped; `--force` looks them up again.
- Optional folders limit the run: `ymlib identify "Michael Jackson/Bad"`.

### `review`
```sh
ymlib review                           # open items with numbered candidates and links
ymlib review apply <id> <n> [--apply]  # tag the files with candidate n
ymlib review skip <id>                 # dismiss without changes
```
Review items keep track of files moved by `organize`, and close automatically when a later `identify` tags those files.

### `retag [--preset NAME] [--apply]`
Rewrites all files with a preset (default: `active_preset`). It removes fields the preset doesn't include and shrinks embedded covers. Before a cover is shrunk or removed, the full-size image is saved to the album folder. It also converts track numbers stored in other formats (`1`, `3/12`) to the zero-padded form.

### `organize [--apply]`
Moves files to the paths given by `path_template`. `cover.jpg` and other folder images move with their album, empty folders are removed, and letter-case-only renames work on macOS. Path conflicts are reported and skipped, never overwritten.

### `singles [as-album|grouped] [--apply]`
- `grouped`: each artist's singles become one album, `<Artist> Singles`, numbered by release date. The original album and track number are remembered.
- `as-album`: every single gets its own album back.

Only tags change; run `organize` afterwards to move the files. A file counts as a single if its release type is `single`, or if it's the only track of an album named like the track (`X`, `X - Single`).

### `config`, `preset`, `auth`
```sh
ymlib config path        # where config, database and cache live
ymlib config show        # effective configuration
ymlib config init [--force]
ymlib preset list        # presets; * marks the active one
ymlib auth discogs       # store the Discogs token (--remove to delete)
ymlib auth status
```

## Configuration

`~/.config/ymlib/config.yaml`. Only the keys you set override the defaults. Unknown keys are reported as errors, so typos don't go unnoticed.

```yaml
library_path: ~/Music/ymlib
active_preset: full                # preset used by identify and retag
path_template: '{albumartist}/{album}/{disc?}{track:02} - {title}.{ext}'
singles: as-album                  # or: grouped
providers: [discogs, musicbrainz]  # lookup order
metadata:
  min_score: 0.85                  # below this, matches go to review
  contact: ""                      # optional URL/email for the MusicBrainz User-Agent
cache:
  max_age_days: 180                # cached lookups older than this are deleted
queue:
  parallel: 2                      # reserved for downloads (not used yet)
presets:
  car:                             # your own presets
    fields: [Title, Artist, Album, Track Number, Picture Cover]
    cover_max_px: 300
```

### Fields and presets

Fields: `Title`, `Album`, `Artist`, `Artists` (all performers), `Album Artist`, `Date`, `Track Number`, `Disc Number`, `Genre`, `Picture Cover`, `Picture Back`.

| Built-in preset | Fields | Embedded covers |
|---|---|---|
| `full` | all | max 600 px |
| `mp3-player` | all except `Artists` and `Picture Back` | max 600 px |

`cover_max_px` limits the longest side of embedded front and back covers, keeping the aspect ratio (`0` = original size). The full-size images are kept as `cover.jpg`/`back.jpg` in the album folder.

A user preset with a built-in's name replaces it. Release IDs (MusicBrainz, Discogs, ISRC) are always kept so files can be recognized again.

Track numbers are stored zero-padded: `01`, `10` (MP3: `01/11`).

### Path template

| Variable | Value |
|---|---|
| `{albumartist}` | Album Artist, falls back to Artist |
| `{artist}`, `{album}`, `{title}`, `{genre}`, `{date}` | tag values |
| `{year}` | year from Date |
| `{track}`, `{disc}` | numbers; `{track:02}` pads to 2 digits |
| `{disc?}` | `2-` on multi-disc albums, empty otherwise |
| `{ext}` | file extension (required at the end) |

Names are made safe for FAT32/exFAT (SD cards, players): characters like `/ \ : * ? " < > |` become `_`, there are no leading dots or trailing dots and spaces, and names have a length limit. Missing values become `Unknown Artist`, `Unknown Album` or `Untitled`.

## Where data lives

| What | Path |
|---|---|
| Config | `~/.config/ymlib/config.yaml` (`$XDG_CONFIG_HOME`) |
| Index and cache database | `~/.local/share/ymlib/library.db` (`$XDG_DATA_HOME`) |
| Cover image cache | `~/.cache/ymlib/covers` (`$XDG_CACHE_HOME`) |
| Discogs token | OS keychain (`YMLIB_DISCOGS_TOKEN` overrides it) |

The database is only an index; your files' tags are the source of truth. Deleting `library.db` loses nothing except cached lookups and open review items: run `ymlib scan` to rebuild it.

## Development

```sh
make test     # go test ./...
make lint     # gofmt check, go vet, staticcheck (pinned in go.mod as a Go tool)
make check    # lint + test
make fmt
```

Tests never touch the network, your home directory or your keychain. Provider responses are recorded fixtures in `internal/metadata/testdata`.

Project docs:
- [SPEC.md](SPEC.md): the original idea.
- [PLAN-stage1.md](PLAN-stage1.md): design decisions.
- [MILESTONES.md](MILESTONES.md): roadmap and status.
- [CLAUDE.md](CLAUDE.md): conventions for AI-assisted development.

## Roadmap

- **M4:** source plugins (external programs speaking JSON over stdin/stdout), local import inbox, persistent download queue, "already in library" detection.
- **M5:** terminal UI with cover art (Kitty/iTerm2 graphics, half-block fallback).
- **Later:** YAML batch download lists, artist discography playlists (`.m3u8`), sync to SD cards, players and phones.
