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
| M2 | Tags read/write, path template, `scan` / `organize` / `retag`, singles modes | ✅ 2026-10-14 |
| M3 | Metadata providers (Discogs → MusicBrainz), cache, Needs Review | ✅ 2026-10-14 |
| M4 | Source plugins, local import inbox, persistent download queue, duplicate flags | ⏳ next |
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
- Test-first. `make check` (gofmt, go vet, staticcheck, tests) must pass before a milestone counts as done. Tests never touch the network or the real home directory.
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
  - M4A: no reliable pure-Go writer. Decided in M2: postponed.
- **Covers in the terminal:** Kitty graphics protocol (Ghostty/Kitty/WezTerm), iTerm2 inline images, truecolor half-block fallback (Apple Terminal). Detected from `TERM_PROGRAM`/`TERM`/`KITTY_WINDOW_ID`, with a `YMLIB_IMG` override. Uses `golang.org/x/image/draw`.

## M1 — Foundations ✅
- `internal/platform`: `Dirs{Config, Cache, Data}` using XDG-style paths on macOS and Linux (`~/.config/ymlib`, `~/.cache/ymlib`, `~/.local/share/ymlib/library.db`) and `ExpandHome`. Windows goes here later.
- `internal/config`: fields (Title, Album, Artist, Artists, Album Artist, Date, Track Number, Disc Number, Genre, Picture Cover, Picture Back). `ParseField` ignores case, spaces, `_` and `-`. Built-in presets `full` (all fields, original cover) and `mp3-player` (no Artists or Picture Back, cover ≤600px). User presets in YAML; a user preset with a built-in's name replaces it. Validation rejects unknown keys and fields, duplicate fields, a bad singles mode, unknown providers, `parallel < 1`, and `max_age_days < 1`. Defaults: `library_path: ~/Music/ymlib`, `active_preset: full`, `singles: as-album`, `providers: [discogs, musicbrainz]`, `queue.parallel: 2`, `cache.max_age_days: 180`.
- `internal/store`: SQLite (`modernc.org/sqlite`, WAL, foreign keys), embedded migrations tracked in `PRAGMA user_version`; refuses a database whose schema is newer than the binary. Migration 0001 creates tables `tracks`, `queue_jobs`, `review_items`, `cache_entries`.
- Commands: `ymlib config path|show|init [--force]`, `ymlib preset list`, global flag `--config`.
- The database is first used by `scan` (M2).

## M2 — Tags, paths, library operations ✅
- `internal/tags`: the `Track` model (all preset fields, plus TrackTotal/DiscTotal, ReleaseType, `Identity` IDs, and OriginalAlbum/OriginalTrack for grouped singles). `Read`/`Write` for FLAC and MP3 (ID3v2.4; v2.3 is readable, including TYER). `Write` replaces only the tags this package manages and keeps all others (ReplayGain, comments, other pictures). FLAC is saved through a temp file and rename.
  - Tag names follow Picard: `ARTISTS`, `ALBUMARTIST`, `RELEASETYPE`, `MUSICBRAINZ_TRACKID`/`_ALBUMID` (MP3: UFID + `TXXX:MusicBrainz Album Id`/`Album Type`), `DISCOGS_RELEASE_ID`, plus custom `YMLIB_SOURCE`, `YMLIB_SOURCE_TRACK_ID`, `YMLIB_ORIGINAL_ALBUM`, `YMLIB_ORIGINAL_TRACK`. Common aliases are read: `ALBUM ARTIST`, `YEAR`, `TOTALTRACKS`, `TOTALDISCS`, `n/total`.
  - Track number is stored zero-padded (at least 2 digits): FLAC `TRACKNUMBER=01` with the total in `TRACKTOTAL=11`, MP3 `TRCK=01/11`. Disc number and totals are not padded. `retag` also rewrites files whose stored text is in another format (`1`, `3/12`), found with `StoredTrackNumber` vs `CanonicalTrackNumber`, since `Read` parses both forms into the same number.
  - Cover fallback: if there is no "Front cover" picture, the first picture of type "Other" counts as the cover (ffmpeg and some rippers write that) and is the one replaced on write.
  - `ApplyPreset` (drop fields not in the preset, shrink the cover to `CoverMaxPx`; IDs are always kept), `ShrinkPicture`, and `Diff` (user-visible field changes, used by all previews).
  - **M4A: postponed.** `scan` lists M4A/OGG/OPUS/... as "unsupported format"; they are not indexed.
- `internal/pathtmpl`: template variables `albumartist` (falls back to artist), `artist`, `album`, `title`, `genre`, `date`, `year`, `track`, `disc`, `ext`; zero-padding `{track:02}`; `{disc?}` gives `N-` only for multi-disc albums. Values are cleaned for FAT32 (`/\:*?"<>|` and control characters become `_`, no leading dot, trimmed spaces and trailing dots, DOS reserved names get `_`, segments ≤255 bytes, NFC). Fallbacks: Unknown Artist/Album/Genre/Year, Untitled. `config` validates the template on load.
- `internal/library` (each operation is Plan → Apply):
  - `Scan`: walks the library and skips hidden files and folders (including macOS `._*` files). Unchanged size+mtime means the file isn't re-read. Rows of deleted files are removed. Never modifies files. Reports unsupported and unreadable files.
  - `PlanOrganize`/`ApplyOrganize`: target paths come from the template. An album is multi-disc if any of its tracks says so. Never overwrites: same-target and existing-file collisions become conflicts (checked case-insensitively, as on macOS/FAT). Folder images (`cover.jpg/png`, `back.jpg/png`, `folder.jpg`) move only when the whole folder moves to one place; when folders merge (e.g. grouped singles), the second image stays put as a conflict. Fixes folder letter case on case-insensitive file systems. Removes emptied folders (a lone `.DS_Store` counts as empty).
  - `PlanRetag`/`ApplyRetag`: applies a preset. Before an embedded cover is shrunk or removed, the full-size image is saved as `cover.jpg`/`back.jpg` if the folder has none. Idempotent.
  - `PlanSingles`/`ApplySingles`: a single is RELEASETYPE=single, or already grouped, or (no release type and) the only track of an album whose name matches the title (`X`, `X - Single`, `X (Single)`). Grouped mode renumbers each artist's singles by date (undated last), sets `<Artist> Singles`, and stores the original album and track. As-album mode restores them. Only tags change; `organize` moves the files.
- Migration `0002_track_totals_and_singles.sql` adds `track_total`, `disc_total`, `release_type`, `original_album`, `original_track`.
- Commands: `ymlib scan`, `ymlib organize [--apply]`, `ymlib retag [--preset X] [--apply]`, `ymlib singles [as-album|grouped] [--apply]`. **Preview is the default; `--apply` executes.** organize/retag/singles scan first.
- Tests: fixtures `internal/testutil/testdata/tiny.{flac,mp3}` (0.1 s, untagged) and the `testutil.CopyAudio`/`testutil.JPEG` helpers. A manual end-to-end run on ffmpeg-tagged files found and fixed two bugs: "Other" cover type, and merged-folder image conflicts.

## M3 — Metadata ✅
- `internal/cache`: `Store` is the two-level cache: an in-memory map while the app runs, read-through to SQLite on a miss, written back by `Flush` (every minute via `Run`, and on exit). On startup, rows older than `cache.max_age_days` are deleted. `Files` keeps cover images as files in `~/.cache/ymlib/covers` with the same age limit. Cache keys are request URLs (tokens live in headers, never in keys).
- `internal/metadata`:
  - `Provider` interface (`Search` → `Candidate`s, `Release` → full `Release`). Shared HTTP client with a per-provider rate limit, retries on 429/5xx (`Retry-After`), `ErrNotFound`, and caching.
  - `Discogs`: token in the `Authorization` header, 1 request/s. Artist names lose Discogs' "(2)" suffixes; credits are joined as printed. Dates like `1987-07-00` become `1987-07`; for the chosen release, `RefineDate` checks the master's main edition for a full date (up to 2 extra requests, cached). Track positions `2-03`/`CD1-7` mean disc 2 track 3 / disc 1 track 7; vinyl `A1…B6` is numbered in order. Release type comes from the format descriptions (Single/EP/Compilation). Genre is `genres[0]` (broad, e.g. "Funk / Soul"). Front cover is the primary image. No back cover, because Discogs doesn't label it.
  - `MusicBrainz`: 1 request/s, User-Agent `ymlib/<version> ( metadata.contact )`. Date is the release group's **original** release date; if that is only `yyyy` or `yyyy-mm`, this edition's date fills it in when it's from the same period (`pickMBDate`). Genre is the most-voted one, formatted ("Contemporary R&B"). Release type comes from the release group (a Compilation secondary type wins). Covers come from the Cover Art Archive (`/front`, `/back`). For a track with no album, it searches releases named like the track (its single) first, then the recording's releases.
  - Matching: `similarity` folds case, accents, typographic quotes, `&`→and and punctuation, and ignores edition noise like "(Remastered 2009)" or "[Deluxe Edition]". `Score` = 0.25 artist + 0.25 album + 0.5 tracks (title, or an ISRC equal = 1). Compilations get ×0.7 unless the album name matches. Durations off by more than 15 s on most tracks give ×0.85. Ties go to the closer track count (standard over deluxe), then the earlier date.
  - **Dates are `yyyy-mm-dd`** (ISO, what ID3v2.4/Vorbis expect). If no source has the full date, the known part (`yyyy` or `yyyy-mm`) is written; a day is never invented. `Identifier.Refine` (the `DateRefiner` interface) runs only for the chosen release: the best match in `identify`, the picked candidate in `review apply`.
  - `Identifier`: providers in config order. Details are fetched for the top 3 hits per provider. **The next provider is tried when the previous one errors, finds nothing, or has no match ≥ `metadata.min_score`** (default 0.85). The result always comes from one release (whole record).
- `internal/secrets`: Discogs token in the OS keychain (`zalando/go-keyring`), `YMLIB_DISCOGS_TOKEN` overrides it. Tests use the mock keychain.
- `internal/library`:
  - `PlanIdentify`/`ApplyIdentify` group files by album (grouped singles by their original album; tracks without an album are looked up alone). Files with a release ID are skipped unless `Force`. `ApplyRelease` replaces all release fields and IDs (clearing the other provider's IDs) and keeps source IDs; for a grouped single it keeps the group's album and numbering and fills OriginalAlbum/OriginalTrack.
  - On apply, covers are downloaded once per album. The full size is saved to `cover.jpg`/`back.jpg` if missing, then the active preset is applied. A failed cover download is a warning; tags are still written.
  - Weak matches become `review_items` (migration `0003_review_groups.sql` adds a `paths` JSON column), storing the full candidates so resolving needs no new lookup. Identifying the same files again replaces the open review. `organize` updates review paths, and a successful identify closes reviews of the files it tagged.
- Config: `metadata.min_score` (0.85) and `metadata.contact` (optional; only sent if you set it).
- Commands: `ymlib identify [folder...] [--force] [--apply]`, `ymlib review`, `ymlib review apply <id> <n> [--apply]`, `ymlib review skip <id>`, `ymlib auth discogs [--remove]`, `ymlib auth status`, `ymlib --version`.
- Tests: recorded real responses in `internal/metadata/testdata` (Discogs releases 120096 and 249504; MusicBrainz searches and the "Off the Wall" release). **The Discogs search fixture is handwritten** (search needs a token), so verify it against the real API once a token is set. A live run against MusicBrainz tagged an album and a lone single correctly; it found and fixed the single-search and review-sync bugs.

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
- M3: MusicBrainz uses typographic apostrophes (`Don’t Stop ’til`), and they are written as-is. Should titles be normalized to ASCII `'` (a config option)?
- M3: Discogs has never been run with a real token; check search results and rate-limit headers.
- Grouped singles: the plan said "album date = earliest single", but a file has only one Date tag and each single keeps its own. Not implemented. Decide whether it's needed (e.g. an ORIGINALDATE-style tag) or drop it.
- Grouped singles: when single folders merge, only the first `cover.jpg` moves and the others stay in their old folders (reported as conflicts). Decide whether to keep, delete or rename them (e.g. `cover-<title>.jpg`).
- Genre/cover exception to whole-record matching (Q20) is still not implemented: with Discogs primary, genre is almost always present; covers are missing only when a provider has none. Decide whether it's still wanted.
- S3: standard vs. deluxe default (Q22) and "playlists only for chosen artists" (Q24) were taken as the recommended defaults because the answers repeated the questions. Confirm both.
- M4A tag writing was postponed in M2: an ffmpeg-based writer later, or a pure-Go library if one matures.
