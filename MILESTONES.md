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
| M4 | Source plugins, local import inbox, persistent download queue, duplicate flags | ✅ 2026-10-14 |
| M5 | TUI v1 | ⏳ next |
| **Later stages** | | |
| S2 | Duplicate detection polished across sources | ⏳ |
| S3 | YAML batch download list | ⏳ |
| S4 | Artist playlists | ⏳ |
| S5 | Device sync: devices made of several storages (internal + SD), automatic or manual split | ⏳ |
| S6+ | Background sync, per-device format conversion, Last.fm/Spotify, command bar | ⏳ |

---

## Conventions (apply to every milestone)
- Go only, module `your-music-library`, binary `ymlib` (`cmd/ymlib`). Commands are defined in `internal/cli` and get injected `platform.Dirs` so tests use temporary directories.
- Test-first. `make check` must pass before a milestone counts as done. Tests never touch the network or the real home directory.
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
- `internal/config`: fields (Title, Album, Artist, Artists, Album Artist, Date, Track Number, Disc Number, Genre, Picture Cover, Picture Back). `ParseField` ignores case, spaces, `_` and `-`. Built-in presets `full` (all fields; embedded covers ≤600px since 2026-10-14) and `mp3-player` (no Artists or Picture Back, cover ≤600px). `cover_max_px` applies to the front and back covers. User presets in YAML; a user preset with a built-in's name replaces it. Validation rejects unknown keys and fields, duplicate fields, a bad singles mode, unknown providers, `parallel < 1`, and `max_age_days < 1`. Defaults: `library_path: ~/Music/ymlib`, `active_preset: full`, `singles: as-album`, `providers: [discogs, musicbrainz]`, `queue.parallel: 2`, `cache.max_age_days: 180`.
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

## M4 — Sources and queue ✅
- `internal/source`: plugin protocol v1 (documented in `source.go`). A plugin is any executable in `sources_dir` (default `~/.config/ymlib/sources`), named by its file name. It's started once per request as `<plugin> <command>` with JSON on stdin and JSON on stdout. Commands: `info`, `search`, `resolve`, `download`. Errors come as non-zero exit + stderr, or `{"error": ...}`. Downloads must stay inside `dest_dir` (enforced). Timeouts: 1 min for queries, 30 min for downloads. `Item` carries id, kind, title, artist, album, numbers, duration, ISRC, explicit, cover URL and quality ("FLAC 24/96"). **No real plugins ship.** The test plugin is `internal/source/testdata/fakesource`, built by `testutil.FakeSourceDir`.
- `library.DupIndex`: tiers are source ID → ISRC/MB recording → normalized artist + title (edition noise removed). The same album (normalized exactly; grouped singles also by their original album) means `InLibrary` ✓; otherwise `OtherEdition` ≈.
- `library.PlanIngest`/`ApplyIngest`, shared by `import` and downloads: read tags → skip duplicates (unless `Force`) → `identifyEntries` (identify core refactored to work on files outside the library) → target paths (multi-disc aware, including library tracks) → conflicts. On apply, each file is copied (moved if it's already in staging) to `<library>/.ymlib-incoming/` (`StagingDir`, hidden so scan skips it), tagged (release, covers, preset; folder images go to the *target* album folder), then renamed into place and indexed. Failures stay in staging; `Move` deletes sources only after success. Reviews point to the final paths.
- Migration `0004_queue_v2.sql` rebuilds `queue_jobs`: unique on (source, kind, source_id), adds `parent_id` (album → tracks), `not_before`, `note` and `file`. `internal/queue`: Enqueue (re-enqueueing a failed job resets it), Claim (atomic `UPDATE … RETURNING`), Complete/Fail (retryable failures back off; `MaxAttempts` = 3), Recover (running tracks → pending after a crash), Retry, Clear (albums only together with their tracks).
- `internal/download.Runner`: albums are resolved into track jobs (album metadata filled into tracks). Tracks queued by reference are resolved first. Duplicate check, then download into `StagingDir/job-<id>`, then `Verify` (format supported, tags parse, decodes with ffmpeg if installed, length within max(3 s, 2%) of the announced duration; without ffmpeg, FLAC length comes from STREAMINFO), then the source's metadata and IDs are written into the file. Standalone tracks are ingested right away; album tracks wait until all have finished, then the album is ingested together (one lookup). `queue.parallel` workers; ingest is serialized. Backoff is 30 s, 2 min, 8 min. Ctrl-C stops cleanly and `queue run` resumes.
- Matching: on equal scores, titles that match the files literally win (a Discogs edition spelling "Rock With You." lost to "Rock With You"; found in a live run).
- Config: `sources_dir`.
- Commands: `ymlib sources`, `ymlib search [--album] [--source S] [--limit N] <query>` (✓/≈ flags), `ymlib download [--album] [--force] [--no-run] <source:id>...`, `ymlib queue [run|retry [id]|clear [--failed]]`, `ymlib import <folder> [--apply] [--move] [--force] [--no-lookup]` (refuses folders inside the library).
- Verified live: fake plugin + real MusicBrainz (album and single), and once with real Discogs (search works against the real API).

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
Decided 2026-10-15 (answers in `ANSWERS.md`, "Sync update").

**Devices and storages**
- A **storage** is one mounted volume, identified by its volume UUID (via `internal/platform`). A **device** is a named group of one or more storages, e.g. a player with internal memory and an SD card. An external SSD is simply a device with one storage.
- The user groups connected storages into a device and orders them (e.g. internal first, then SD).
- Device profile (`config.yaml`): mode `mirror | update`, `include_folder_covers` (bool), `allocation: auto | manual`, and `pins`. Each storage in it has a volume UUID, a target folder and `reserve_mb`, which is free space left for the player's own database.
- No per-device metadata preset (rejected): devices get exact copies of library files.

**Manifests**
- Each storage has its own manifest, `.yml-sync.json`, in its target folder. It records the device and storage name, plus relative path, size, mtime and hash for each file. Files are hashed only when their size or mtime changed.
- Manifests on the device are the source of truth for what is where, so the current split is read from the storages themselves and isn't kept in SQLite.

**Allocation: which storage gets what**
- **Unit = artist**, keyed by `{albumartist}`. All of an artist's music goes to one storage, which keeps folder browsing and S4 artist playlists (relative paths) on a single storage. Only an artist too big for any one storage is split, by whole album.
- **`auto`**: new artists go to the first storage, in the device's order, that has room after `reserve_mb`.
- **Sticky:** music already on the device stays where it is. Nothing is reshuffled to pack space better. Moving an artist means a pin or an explicit `ymlib sync move`.
- **Pins:** `pins: {"<Album Artist>": <storage>}` (an album can be pinned too) override `auto` placement.
- **`manual`**: only pinned music syncs. Unpinned artists are listed as "unassigned" in the preview and the TUI.
- A move between storages copies to the new storage, verifies the copy, then deletes the old copy. It is allowed in both modes, but only for files the manifest shows the app copied.

**Fit check before copying**
- The plan works out the whole allocation and checks free space on every storage before anything is copied. This includes the space freed by deletions and moves, minus `reserve_mb`.
- The preview shows, per storage, what will be copied, moved and deleted, the space used before and after, and a **"doesn't fit"** list of albums and artists with their sizes.
- Albums are never copied partially.

**Partial connection**
- When only some of a device's storages are mounted, each connected storage gets its own updates: changed and renamed files, plus deletions in `mirror` mode.
- New music is copied only if its planned storage is connected; otherwise it waits.
- Nothing moves between storages unless both are connected. The preview names the missing storages.

**Modes and running**
- `mirror` deletes only inside a storage's target folder. `update` copies new and changed files, and moves renamed ones, only for files the manifest shows the app copied.
- Runs in the TUI (progress in a corner) plus `ymlib sync [device] [--apply]`, `ymlib device add|list|remove`, `ymlib sync move <artist> <storage>`. Sync code has no UI dependency.
- In `internal/devsync` (not `sync`, which clashes with the standard library): `PlanSync` (allocation, fit, operations) → `ApplySync`. The allocation is a pure function of the library, the manifests, the free space and the profile, so it can be tested without disks.

## S6+ — Later ideas ⏳
- Background sync toggle (launchd on macOS, systemd on Linux; runs when a device is plugged in).
- Per-device format conversion (e.g. FLAC → MP3 via ffmpeg).
- Last.fm and Spotify metadata providers (Spotify API access for new apps is restricted).
- Claude Code-style command bar in the TUI (pitfalls discussed: key-focus modes, quoting, one shared command registry for CLI and TUI, per-command autocomplete, async jobs, history, plain text = search).
- Windows support via `internal/platform`.

## Open questions
- M3: MusicBrainz uses typographic apostrophes (`Don’t Stop ’til`), and they are written as-is. Should titles be normalized to ASCII `'` (a config option)?
- Grouped singles: the plan said "album date = earliest single", but a file has only one Date tag and each single keeps its own. Not implemented. Decide whether it's needed (e.g. an ORIGINALDATE-style tag) or drop it.
- Grouped singles: when single folders merge, only the first `cover.jpg` moves and the others stay in their old folders (reported as conflicts). Decide whether to keep, delete or rename them (e.g. `cover-<title>.jpg`).
- Genre/cover exception to whole-record matching (Q20) is still not implemented: with Discogs primary, genre is almost always present; covers are missing only when a provider has none. Decide whether it's still wanted.
- S3: standard vs. deluxe default (Q22) and "playlists only for chosen artists" (Q24) were taken as the recommended defaults because the answers repeated the questions. Confirm both.
- M4A tag writing was postponed in M2: an ffmpeg-based writer later, or a pure-Go library if one matures.
- S5: when the fit check finds music that doesn't fit, should `--apply` sync everything that does fit and report the rest (current assumption), or refuse until the plan fits completely?
- S5: an artist split by album across two storages can't have one relative-path `.m3u8` playlist. Should it get one playlist per storage, or none?
