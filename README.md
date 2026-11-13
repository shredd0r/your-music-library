# ymlib: your music library

A terminal app for keeping a local music collection clean: it indexes your files, looks up correct metadata on **Discogs** and **MusicBrainz**, writes tags in one consistent format, downloads covers, sorts files into an `Artist/Album/01 - Title.flac` layout, and downloads music through plugin servers you run.

> **Status: `0.1.0-dev`.** Tagging, tidying, metadata lookup, importing, downloading and the terminal UI work. Batch downloads, playlists and device sync are planned. See [MILESTONES.md](MILESTONES.md).

ymlib is a terminal UI only: run `ymlib` (no arguments) in a terminal.

**Nothing changes without asking.** Everything that modifies files shows a preview first and acts only when you confirm. Files are never overwritten. Tags ymlib doesn't manage (ReplayGain, lyrics, comments) are kept.

Supported formats: **FLAC** and **MP3** (ID3v2.4). Other audio files (M4A, OGG, OPUS, …) are left alone.

## Requirements

- macOS or Linux, and a terminal
- Go 1.26+ to build
- Optional: a [Discogs personal access token](https://www.discogs.com/settings/developers), used as the primary metadata source. Without one, MusicBrainz is used on its own.

## Build and start

```sh
make build          # → bin/ymlib
./bin/ymlib
```

On first start ymlib writes `~/.config/ymlib/config.yaml` with the defaults and creates the library folder `~/Music/ymlib`. To use another folder, set `library_path` in that file and start again.

**Discogs token (optional).** ymlib reads it from the OS keychain, or from `YMLIB_DISCOGS_TOKEN`, which wins. It never stores it itself; add it once with your OS tools:

```sh
# macOS
security add-generic-password -s ymlib -a discogs-token -w <token>
# Linux (Secret Service)
secret-tool store --label="ymlib Discogs token" service ymlib username discogs-token
```

Settings shows whether a token was found.

## Screens

| Screen | What you do there |
|---|---|
| **1 Search** | `/` to type a query, `ctrl+t` to switch between tracks and albums, `enter` to search. Results show ✓ in library, ≈ other edition, ↓ queued, plus details and the cover. `enter` or `d` downloads. Offline plugins are skipped with a warning. |
| **2 Queue** | Jobs and the download log. `s` start/resume, `x` stop, `r`/`R` retry one/all failed, `c`/`C` clear finished (and failed). Jobs marked `…` wait for an offline or disabled plugin. |
| **3 Library** | Albums (`/` filters by artist, album or track title), tracks, tags and cover. See [Library actions](#library-actions). |
| **4 Review** | Albums without a confident match: your files next to each candidate release (`←→`), with cover and tracklist. `enter` previews, `y` writes, `s` skips. |
| **5 Settings** | Configuration (with the path of `config.yaml`), the Discogs token status, and plugins (online/offline). `enter` enables or disables a plugin, `a` adds one (name and URL), `x` removes one; changes are saved to `config.yaml` with your comments kept. `r` checks the plugins again. |

`tab` or `1`–`5` switch screens, `q` quits, `esc` leaves a text box or cancels a preview. Quitting stops downloads; they resume next time. Plugins are checked every 15 seconds; when one comes back online, downloads waiting for it start.

Covers are real images in **Kitty** and **Ghostty**. Other terminals (iTerm2, Apple Terminal, inside tmux) get colored half-blocks. Set `YMLIB_IMAGES=kitty` or `YMLIB_IMAGES=halfblock` to choose.

### Library actions

Each action shows a preview; `enter` (or `y`) applies it, `esc` cancels, `↑↓` scrolls.

| Key | Action |
|---|---|
| `s` / `S` | Rescan the library folder / rebuild the index by rereading every file. The index follows the files: new and changed files are read, missing ones dropped. Hidden files and macOS `._*` files are skipped. |
| `e` | Edit the album's or the track's tags (preview first). Text is saved in the [fixed format](#tags). |
| `i` / `I` | Identify the selected album / every album without a release ID. See [Identify](#identify). |
| `t` | **Tidy library**: bring every file to the [fixed tag format](#tags), then move files to their `path_template` location. One preview lists the tag changes and then the file moves, computed from the new tags. Before an embedded cover is shrunk, the full-size image is saved as `cover.jpg`. If a playlist in `<library>/Playlists/` lists a file that moves, the preview warns: *playlists could be broken*. |
| `a` | **Import** a folder from outside the library (CD rips, purchases): tracks already in the library are skipped, albums are identified, and files are tagged and filed by `path_template`. `enter` copies; `m` imports and deletes the originals. |

### Identify

Files are grouped by album and each album is looked up: Discogs first, then MusicBrainz if Discogs fails, finds nothing, or finds nothing confident.

- **Whole-record matching:** every tag of a file comes from a single release, never a mix of sources. The one exception: when the chosen release has no genre or no front cover, the next provider may fill them in, but only from a release that also scores at least `min_score` against your files.
- Matching ignores case, accents, typographic quotes, guest credits ("(feat. X)") and edition suffixes like "(Remastered 2009)". Compilations are scored down so a single isn't mistaken for track 7 of a "Hits of 1987" album.
- Matches scoring at least `metadata.min_score` (default 0.85) are applied. Weaker ones go to the Review screen.
- Dates are written as `yyyy-mm-dd` using the original release date. When a source only knows the year, the app looks further (the edition's date on MusicBrainz, the main edition on Discogs). A day is never invented.
- The front cover is downloaded once per album, saved full size as `cover.jpg`, and embedded at most 600 px. Back covers aren't used.
- Files that already carry a release ID are skipped.

### Downloads

Downloads come from **plugins**: HTTP servers you run yourself, in any language. ymlib doesn't include any download source; it only keeps each plugin's address (add it in Settings) and talks to it (`GET /health`, `POST /search`, `POST /resolve`, `POST /download`, which streams the file). The protocol, with a minimal Python plugin, is in [docs/plugin-protocol.md](docs/plugin-protocol.md). Plugin authors are responsible for using services in line with their terms.

How a download proceeds:
1. Albums are split into tracks, and tracks already in the library are skipped.
2. Each track is downloaded into a hidden staging folder inside the library.
3. The file is checked: it must decode (with ffmpeg, if installed), and its length must match what the source announced.
4. Temporary failures are retried up to 3 times (after 30 s, 2 min, 8 min). When the plugin says the item is not found or permanently unavailable, the job fails right away.
5. If the plugin is offline or disabled, the job waits without using up attempts, and starts when the plugin is back. After `plugins.offline_wait_minutes` it fails; retry it on the Queue screen.
6. When all tracks of an album are in, the album is identified once, tagged in the fixed format and filed by `path_template`. Nothing half-finished ever appears in the library.

## Configuration

`~/.config/ymlib/config.yaml`, written with the defaults on first start. Only the keys you set override the defaults. Unknown keys are reported as errors, so typos don't go unnoticed. Changes take effect at the next start.

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
  registered:                      # managed in Settings
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

The database is only an index; your files' tags are the source of truth. Deleting `library.db` loses only cached lookups, open review items and the download queue; the index is rebuilt from your files on the next start.

## Development

```sh
make test     # go test ./...
make lint     # golangci-lint (installed into bin/ by `make install-lint`)
make check    # lint + test
make fmt
```

`ymlib` needs a terminal, so automated tests drive the UI through `App.Update` (`internal/tui/tui_test.go`) instead of a real terminal.

Tests never touch the network, your home directory or your keychain. Provider responses are recorded fixtures in `internal/metadata/testdata`. Download tests use a fake plugin server running inside the test process (`internal/source/sourcetest`).

Project docs:
- [SPEC.md](SPEC.md): the original idea.
- [PLAN-stage1.md](PLAN-stage1.md): design decisions.
- [MILESTONES.md](MILESTONES.md): roadmap and status.
- [docs/plugin-protocol.md](docs/plugin-protocol.md): how to write a download plugin.
- [CLAUDE.md](CLAUDE.md): conventions for AI-assisted development.

## Roadmap

- **Next stages:** YAML batch download lists, artist discography playlists (`.m3u8`), sync to SD cards, players and phones (including players with internal memory plus an SD card, split automatically or by hand).
