# ymlib: your music library

A command-line and terminal tool for keeping a local music collection clean: it indexes your files, looks up correct metadata on **Discogs** and **MusicBrainz**, writes tags in the format you choose, downloads covers, and sorts files into a consistent `Artist/Album/01 - Title.flac` layout.

> **Status: `0.1.0-dev`, Stage 1 complete.** Tagging, organizing, metadata lookup, importing, downloading through plugin servers you run, and the terminal UI work. Playlists and device sync are planned. See [MILESTONES.md](MILESTONES.md).

## What it does today

| Feature | Command |
|---|---|
| Terminal UI: search and download, queue, library browser with covers and tag editing, review, settings | `ymlib` |
| Index your music folder (reads tags only, changes nothing) | `ymlib scan` |
| Identify albums on Discogs/MusicBrainz and write tags and covers | `ymlib identify` |
| Choose a release by hand when a match isn't certain | `ymlib review` |
| Bring existing files to the fixed tag format (see [Tags](#tags)) | `ymlib retag` |
| Rename and move files according to a path template | `ymlib organize` |
| Import a folder (rips, purchases) into the library | `ymlib import` |
| Register download plugins (HTTP servers you run) | `ymlib plugin` |
| Search and download through those plugins | `ymlib search`, `ymlib download`, `ymlib queue` |

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

## Terminal UI

Run `ymlib` with no arguments, or `ymlib tui`:

| Screen | What you do there |
|---|---|
| **1 Search** | `/` to type a query, `ctrl+t` to switch between tracks and albums, `enter` to search. Results show ✓ in library, ≈ other edition, ↓ queued, plus details and the cover. `enter` or `d` downloads. Offline plugins are skipped with a warning. |
| **2 Queue** | Jobs and the download log. `s` start/resume, `x` stop, `r`/`R` retry one/all failed, `c`/`C` clear finished (and failed). Jobs marked `…` wait for an offline or disabled plugin. |
| **3 Library** | Albums (`/` filters by artist, album or track title), tracks, tags and cover. `e` edits the album's or the track's tags: preview first, `y` to write. `s` rescans. |
| **4 Review** | Albums without a confident match: your files next to each candidate release (`←→`), with cover and tracklist. `enter` previews, `y` writes, `s` skips. |
| **5 Settings** | Configuration (with the path of `config.yaml`) and plugins (online/offline). `enter` enables or disables a plugin, saved to `config.yaml` with your comments kept. `r` checks the plugins again. |

`tab` or `1`–`5` switch screens, `q` quits, `esc` leaves a text box. Quitting stops downloads; they resume next time. Plugins are checked every 15 seconds; when one comes back online, downloads waiting for it start.

Covers are real images in **Kitty** and **Ghostty**. Other terminals (iTerm2, Apple Terminal, inside tmux) get colored half-blocks. Set `YMLIB_IMAGES=kitty` or `YMLIB_IMAGES=halfblock` to choose.

## Commands

### `scan`
Walks `library_path`, reads tags and updates the index (SQLite). Files whose size and modification time haven't changed aren't read again. Hidden files and macOS `._*` files are skipped. `organize`, `retag` and `identify` scan automatically first.

### `identify [folder...] [--force] [--apply]`
Groups files by album and looks each album up. It tries Discogs first, then MusicBrainz if Discogs fails, finds nothing, or finds nothing confident.

- **Whole-record matching:** every tag of a file comes from a single release, never a mix of sources.
- Matching ignores case, accents, typographic quotes, guest credits ("(feat. X)") and edition suffixes like "(Remastered 2009)". Compilations are scored down so a single isn't mistaken for track 7 of a "Hits of 1987" album.
- Matches scoring at least `metadata.min_score` (default 0.85) are applied. Weaker ones go to `ymlib review`.
- Dates are written as `yyyy-mm-dd` using the original release date. When a source only knows the year, the app looks further (the edition's date on MusicBrainz, the main edition on Discogs). A day is never invented.
- When the chosen release has no genre or no front cover, the next provider may fill them in, but only from a release that also scores at least `min_score` against your files. Otherwise the field stays empty.
- The front cover is downloaded once per album. The full-size image is saved as `cover.jpg` in the album folder; the embedded copy is at most 600 px. Back covers aren't used.
- Files that already carry a release ID are skipped; `--force` looks them up again.
- Optional folders limit the run: `ymlib identify "Michael Jackson/Bad"`.

### `review`
```sh
ymlib review                           # open items with numbered candidates and links
ymlib review apply <id> <n> [--apply]  # tag the files with candidate n
ymlib review skip <id>                 # dismiss without changes
```
Review items keep track of files moved by `organize`, and close automatically when a later `identify` tags those files.

### `retag [--apply]`
Brings every file to the fixed [tag format](#tags): removes track/disc totals and embedded back covers, pads numbers (`1`, `3/12` → `01`, `03`), converts typographic quotes and dashes to ASCII, moves guests from Artist into the title, restores singles that an older version grouped into `<Artist> Singles`, and shrinks embedded covers. Before a cover is shrunk, the full-size image is saved as `cover.jpg` in the album folder. Tags ymlib doesn't manage (ReplayGain, lyrics, comments) are kept. Run `organize` afterwards, since titles and numbers may change file names.

### `organize [--apply]`
Moves files to the paths given by `path_template`. `cover.jpg` and other folder images move with their album, empty folders are removed, and letter-case-only renames work on macOS. Path conflicts are reported and skipped, never overwritten.

### `import <folder> [--apply] [--move] [--force] [--no-lookup]`
Brings audio files from outside the library in (CD rips, Bandcamp downloads, …).
- Tracks already in the library are skipped (`--force` imports them anyway).
- Albums are identified like `identify`, and files are tagged in the fixed format and copied to their `path_template` location.
- `--move` deletes the originals after a successful import. `--no-lookup` keeps the files' own tags.

### Downloads: `plugin`, `search`, `download`, `queue`
```sh
ymlib plugin add <name> <url>          # register a plugin server you run
ymlib plugin list                      # registered plugins, online/offline
ymlib plugin enable|disable|remove <name>
ymlib search rock with you             # tracks; ✓ = in library, ≈ = other edition in library
ymlib search --album off the wall
ymlib search --plugin NAME <query>     # only this plugin (also when disabled)
ymlib download myplugin:t1             # <plugin>:<id> from the search results
ymlib download --album myplugin:a1
ymlib queue                            # jobs and results
ymlib queue run                        # resume after Ctrl-C or a crash
ymlib queue retry [job-id]             # failed jobs again
ymlib queue clear [--failed]
```
How a download proceeds:
1. Albums are split into tracks, and tracks already in the library are skipped.
2. Each track is downloaded into a hidden staging folder inside the library.
3. The file is checked: it must decode (with ffmpeg, if installed), and its length must match what the source announced.
4. Temporary failures are retried up to 3 times (after 30 s, 2 min, 8 min). When the plugin says the item is not found or permanently unavailable, the job fails right away.
5. If the plugin is offline or disabled, the job waits without using up attempts. The next `ymlib queue run` (or the TUI, when the plugin is back) tries again; after `plugins.offline_wait_minutes` it fails, and `ymlib queue retry` brings it back.
6. When all tracks of an album are in, the album is identified once, tagged in the fixed format and filed by `path_template`. Nothing half-finished ever appears in the library.

**Source plugins.** ymlib doesn't include any download source. A plugin is an **HTTP server you run yourself**, in any language; ymlib only keeps its address and talks to it (`GET /health`, `POST /search`, `POST /resolve`, `POST /download`, which streams the file). Register one with `ymlib plugin add <name> http://127.0.0.1:<port>`. If a plugin is offline, searches skip it and its queued downloads wait (up to `plugins.offline_wait_minutes`) instead of failing. The protocol, with a minimal Python plugin, is in [docs/plugin-protocol.md](docs/plugin-protocol.md). Plugin authors are responsible for using services in line with their terms.

### `config`, `auth`
```sh
ymlib config path        # where config, database and cache live
ymlib config show        # effective configuration
ymlib config init [--force]
ymlib auth discogs       # store the Discogs token (--remove to delete)
ymlib auth status
```

## Configuration

`~/.config/ymlib/config.yaml`. Only the keys you set override the defaults. Unknown keys are reported as errors, so typos don't go unnoticed.

```yaml
library_path: ~/Music/ymlib
path_template: '{albumartist}/{album}/{disc?}{track:02} - {title}.{ext}'
providers: [discogs, musicbrainz]  # lookup order
metadata:
  min_score: 0.85                  # below this, matches go to review
  contact: ""                      # optional URL/email for the MusicBrainz User-Agent
cache:
  max_age_days: 180                # cached lookups older than this are deleted
queue:
  parallel: 2                      # simultaneous downloads
plugins:
  offline_wait_minutes: 60         # downloads of an offline or disabled plugin wait this long, then fail
  registered:                      # managed by `ymlib plugin add|remove|enable|disable`
    myplugin: {url: "http://127.0.0.1:8765", enabled: true}
```

Presets (`active_preset`, `presets`) and the singles mode (`singles`) were removed. If your file still has them, ymlib deletes those keys on the next start (keeping your comments) and says so once.

### Tags

The tag format is fixed; there are no presets. Every file gets:

| Field | Notes |
|---|---|
| Title | a guest moves here from Artist: `Bad (feat. X)` |
| Album, Album Artist, Genre, Date | Date is `yyyy-mm-dd`, or `yyyy` / `yyyy-mm` when that's all that's known |
| Artist | the main artist only, so players group correctly |
| Artists | every performer (multiple values) |
| Track Number, Disc Number | two digits, no totals: `01` (MP3 `TRCK=01`, FLAC `TRACKNUMBER=01`) |
| Picture Cover | the front cover, at most 600 px; full size in `cover.jpg` |

- Text is ASCII for quotes, dashes and ellipses: `’` → `'`, `“ ”` → `"`, `– —` → `-`, `…` → `...`.
- Guests are split only on `feat.`, `ft.` and `featuring`, never on `&`, so `Simon & Garfunkel` stays one artist.
- Release IDs (MusicBrainz, Discogs, ISRC) and the plugin's source IDs are always written, so files can be recognized again.
- Back covers and track/disc totals are not written. Tags ymlib doesn't manage (ReplayGain, lyrics, comments) are kept.

### Path template

| Variable | Value |
|---|---|
| `{albumartist}` | Album Artist, falls back to Artist |
| `{artist}`, `{album}`, `{title}`, `{genre}`, `{date}` | tag values |
| `{year}` | year from Date |
| `{track}`, `{disc}` | numbers; `{track:02}` pads to 2 digits |
| `{disc?}` | `02-` on multi-disc albums (two digits), empty otherwise. An album counts as multi-disc when its files have a disc number of 2 or more. |
| `{ext}` | file extension (required at the end) |

Names are made safe for FAT32/exFAT (SD cards, players): characters like `/ \ : * ? " < > |` become `_`, there are no leading dots or trailing dots and spaces, and names have a length limit. Missing values become `Unknown Artist`, `Unknown Album` or `Untitled`.

## Where data lives

| What | Path |
|---|---|
| Config, including registered plugins | `~/.config/ymlib/config.yaml` (`$XDG_CONFIG_HOME`) |
| Index and cache database | `~/.local/share/ymlib/library.db` (`$XDG_DATA_HOME`) |
| Cover image cache | `~/.cache/ymlib/covers` (`$XDG_CACHE_HOME`) |
| Discogs token | OS keychain (`YMLIB_DISCOGS_TOKEN` overrides it) |
| Download staging | `<library>/.ymlib-incoming/` (hidden; files that failed to import stay here) |

The database is only an index; your files' tags are the source of truth. Deleting `library.db` loses only cached lookups, open review items and the download queue: run `ymlib scan` to rebuild the index.

## Development

```sh
make test     # go test ./...
make lint     # golangci-lint (installed into bin/ by `make install-lint`)
make check    # lint + test
make fmt
```

Tests never touch the network, your home directory or your keychain. Provider responses are recorded fixtures in `internal/metadata/testdata`. Download tests use a fake plugin server running inside the test process (`internal/source/sourcetest`).

Project docs:
- [SPEC.md](SPEC.md): the original idea.
- [PLAN-stage1.md](PLAN-stage1.md): design decisions.
- [MILESTONES.md](MILESTONES.md): roadmap and status.
- [docs/plugin-protocol.md](docs/plugin-protocol.md): how to write a download plugin.
- [CLAUDE.md](CLAUDE.md): conventions for AI-assisted development.

## Roadmap

- **Next stages:** YAML batch download lists, artist discography playlists (`.m3u8`), sync to SD cards, players and phones (including players with internal memory plus an SD card, split automatically or by hand).
