# ymlib — Milestones

This is the working roadmap. Read it at the start of a session to know where the project stands.
- Design decisions and their reasons: `PLAN-stage1.md` (settled decisions) and `ANSWERS.md` (raw answers from the grilling session).
- Original idea: `SPEC.md`.

Status legend: ✅ done · 🚧 in progress · ⏳ not started

| # | Milestone | Status |
|---|-----------|--------|
| **Stage 1 (MVP: feature 1)** | | |
| M0 | Library spikes: tags + terminal covers | ✅ 2026-10-13 |
| M1 | Platform dirs, config + presets, SQLite store, `config`/`preset` commands | ✅ 2026-10-13 |
| M2 | Tags read/write, path template, `scan` / `organize` / `retag`, singles modes | ⏳ next |
| M3 | Metadata providers (Discogs → MusicBrainz), cache, Needs Review | ⏳ |
| M4 | Source plugins, local import inbox, persistent download queue, duplicate flags | ⏳ |
| M5 | TUI v1 | ⏳ |
| **Later stages** | | |
| S2 | Duplicate detection polished across sources | ⏳ |
| S3 | YAML batch download list | ⏳ |
| S4 | Artist playlists | ⏳ |
| S5 | Device sync | ⏳ |
| S6+ | Background sync, per-device format conversion, Last.fm/Spotify, command bar | ⏳ |

---

## Conventions (apply to every milestone)
- Go only, module `your-music-library`, binary `ymlib` (`cmd/ymlib`). Commands are defined in `internal/cli` and get injected `platform.Dirs` so tests use temporary directories.
- Test-first. `go vet ./... && go test ./...` must pass before a milestone counts as done. Tests never touch the network or the real home directory.
- File tags are the source of truth. SQLite is only an index, rebuildable with `scan`.
- Nothing moves or gets re-tagged without a preview (`--dry-run` shows the plan, applying is explicit).
- No browser in core. No integration with unlicensed sources (lucida.to, monochrome.st and similar). Sources are external plugins.
- `spikes/` are separate throwaway Go modules, not part of the app.
- Schema changes are new numbered files `internal/store/migrations/NNNN_name.sql`. Never edit an applied migration.

---

## M0 — Spikes ✅
Goal: choose libraries before building on them. Code lives in `spikes/tagspike`, `spikes/imgspike`.
- **Tags:** `go-flac/go-flac/v2` + `flacvorbis/v2` + `flacpicture/v2` for FLAC; `bogem/id3v2/v2` (v2.4, UTF-8) for MP3; `dhowden/tag` for reading. All fields, front/back pictures (types 3/4), and multi-value Artists work.
  - Artists: FLAC uses repeated `ARTISTS` comments. MP3 uses `TXXX:ARTISTS` with null-separated values (Picard convention); ffprobe shows only the first value, but nothing is lost.
  - M4A: no reliable pure-Go writer. Decide in M2: an ffmpeg-based writer behind `TagWriter`, or postpone.
- **Covers in the terminal:** Kitty graphics protocol (Ghostty/Kitty/WezTerm), iTerm2 inline images, truecolor half-block fallback (Apple Terminal). Detected from `TERM_PROGRAM`/`TERM`/`KITTY_WINDOW_ID`, with a `YMLIB_IMG` override. Uses `golang.org/x/image/draw`.

## M1 — Foundations ✅
- `internal/platform`: `Dirs{Config, Cache, Data}` using XDG-style paths on macOS and Linux (`~/.config/ymlib`, `~/.cache/ymlib`, `~/.local/share/ymlib/library.db`) and `ExpandHome`. Windows goes here later.
- `internal/config`: fields (Title, Album, Artist, Artists, Album Artist, Date, Track Number, Disc Number, Genre, Picture Cover, Picture Back). `ParseField` ignores case, spaces, `_` and `-`. Built-in presets `full` (all fields, original cover) and `mp3-player` (no Artists or Picture Back, cover ≤600px). User presets in YAML; a user preset with a built-in's name replaces it. Validation rejects unknown keys and fields, duplicate fields, a bad singles mode, unknown providers, `parallel < 1`, and `max_age_days < 1`. Defaults: `library_path: ~/Music/ymlib`, `active_preset: full`, `singles: as-album`, `providers: [discogs, musicbrainz]`, `queue.parallel: 2`, `cache.max_age_days: 180`.
- `internal/store`: SQLite (`modernc.org/sqlite`, WAL, foreign keys), embedded migrations tracked in `PRAGMA user_version`; refuses a database whose schema is newer than the binary. Migration 0001 creates tables `tracks`, `queue_jobs`, `review_items`, `cache_entries`.
- Commands: `ymlib config path|show|init [--force]`, `ymlib preset list`, global flag `--config`.
- Not yet: no command opens the database (first user: `scan` in M2).

## M2 — Tags, paths, library operations ⏳ (next)
- `internal/tags`: `Track` model (all fields plus identity: source, source_track_id, ISRC, MB recording/release IDs, Discogs release ID, stored in custom tags). `TagReader` / `TagWriter` interfaces; FLAC + MP3 implementations. A writer writes **only the preset's fields** and removes the ones the preset excludes. Embedded cover resized to `CoverMaxPx` (JPEG). Decide on M4A.
- `internal/pathtmpl`: render the template `{albumartist}/{album}/{disc?}{track:02} - {title}.{ext}`. `{disc?}` gives `D-` only on multi-disc albums. FAT32-safe name cleaning (`/ \ : * ? " < > |`, control characters, trailing dots and spaces, reserved names, length limit). Validate the template when loading the config. Collisions get a suffix or an error, never an overwrite.
- `internal/library`:
  - `scan`: walk `library_path`, read tags, upsert into `tracks` (skip files whose size and mtime haven't changed), remove rows for deleted files. Never modifies files.
  - `organize --dry-run | --apply`: compute target paths from tags plus the template; move files, `cover.jpg` and `back.jpg`; clean up empty folders.
  - `retag --preset X --dry-run | --apply`: show per-file field changes, then write.
  - Singles: `as-album` vs. `grouped` ("`<Artist>` Singles", numbered by date, each file keeps its own Date, album date = earliest single, renumber when a new single is added); a command to convert between modes (preview first).
- Covers: `cover.jpg` / `back.jpg` saved full size in the album folder; the embedded copy follows the preset.
- Tests: generated FLAC/MP3 fixtures (ffmpeg in a test helper, or committed tiny files), round-trip tags, golden path tests, dry-run makes no changes.

## M3 — Metadata ⏳
- `internal/metadata`: `Provider` interface. Discogs client (personal token, 60 req/min) and MusicBrainz client (1 req/s, required User-Agent), plus Cover Art Archive.
- Chain, whole-record: Discogs is primary, MusicBrainz is used when Discogs finds nothing or errors. Exception still to confirm (Q20): Genre and covers only, from the next provider, if the same release matches by barcode or catalog number.
- Confidence scoring: auto-apply above a threshold, otherwise create a `review_items` row with candidates. Command: `ymlib review` (CLI list/resolve; TUI screen in M5).
- `internal/cache`: in-memory map during a run, flushed to `cache_entries` on exit and periodically (crash safety), loaded on start, purge entries older than `cache.max_age_days` on startup. Cover images cached as files in `Dirs.Cache`.
- Credentials: OS keychain (`zalando/go-keyring`), env var alternative (e.g. `YMLIB_DISCOGS_TOKEN`).
- Tests: recorded HTTP fixtures (`httptest`), no network.

## M4 — Sources and queue ⏳
- `internal/source`: plugin protocol, JSON over stdin/stdout, with operations `search` (results: title, artist, album, length, quality, explicit, cover URL, source ID), `resolve`, and `download` (into a given temp folder). Plugins are found in a configured folder. Fake plugin binary built in tests.
- Built-in source `localimport`: an inbox folder of audio files; identify via M3, tag with the active preset, file into the library with the template.
- `internal/queue`: jobs saved in `queue_jobs`, `queue.parallel` workers, retry with backoff, download into a temp folder, then check (file decodes, duration matches), tag, move into the library, index. Nothing half-done ever enters the library.
- Duplicate flags on search results: ✓ in library (source ID → ISRC/MBID → normalized albumartist+album+disc+track+title), ≈ other edition.
- Commands: `ymlib search`, `ymlib download`, `ymlib import <dir>`, `ymlib queue`.

## M5 — TUI v1 ⏳
- Bubble Tea. Screens: Search & Download (results with cover, ✓/≈ flags), Queue, Library browser (tags, cover viewer, edit fields), Needs Review (candidates side by side), Settings/Presets. Devices & Sync screen arrives with S5.
- Cover renderer from the M0 spike (Kitty / iTerm2 / half-block).
- The TUI calls the same `internal/*` packages as the CLI; no logic lives only in the TUI.

---

## S2 — Duplicate detection across sources ⏳
Tune the matching tiers once real sources and Discogs data exist (Discogs rarely has ISRC/MBID, so tier 3 does more of the work).

## S3 — YAML batch list ⏳
```yaml
prefer_remasters: false
artists:
  - name: Michael Jackson
    prefer_remasters: true   # optional per-artist override
    albums: [Bad, Off the Wall]
    singles: [Some Single]
```
- Ranking: (1) title/artist match, (2) explicit over clean, (3) original vs. remaster per `prefer_remasters`, (4) quality (bit depth, then sample rate), (5) standard over deluxe. Ties or unclear results go to Needs Review.
- Each entry ends up queued, already in library, or needs review, with a summary report.

## S4 — Artist playlists ⏳
- `<library>/Playlists/Its <Artist>.m3u8`, relative paths, only for artists the user picks.
- Order: album date → disc → track; singles placed by their own date. Regenerated automatically when that artist's files change.

## S5 — Device sync ⏳
- Device identified by volume UUID (via `internal/platform`). Profile: target folder, mode `mirror | update`, `include_folder_covers` (bool). No per-device metadata preset (rejected): devices get exact copies of library files.
- Manifest `.yml-sync.json` on the device (relative path, size, mtime, hash); files are hashed only when their size or mtime changed.
- `mirror` deletes only inside the target folder. `update` copies new and changed files, and moves renamed ones, only for files the manifest shows the app copied.
- Runs in the TUI (progress in a corner) plus `ymlib sync`. Sync code has no UI dependency.

## S6+ — Later ideas ⏳
- Background sync toggle (launchd on macOS, systemd on Linux; runs when a device is plugged in).
- Per-device format conversion (e.g. FLAC → MP3 via ffmpeg).
- Last.fm and Spotify metadata providers (Spotify API access for new apps is restricted).
- Claude Code-style command bar in the TUI (pitfalls discussed: key-focus modes, quoting, one shared command registry for CLI and TUI, per-command autocomplete, async jobs, history, plain text = search).
- Windows support via `internal/platform`.

## Open questions
- M3: confirm the Genre/cover exception to whole-record matching (Q20 was answered by moving Discogs to primary; the exception itself is unconfirmed).
- S3: standard vs. deluxe default (Q22) and "playlists only for chosen artists" (Q24) were taken as the recommended defaults because the answers repeated the questions. Confirm both.
- M2: M4A tag writing (ffmpeg writer or postpone).
