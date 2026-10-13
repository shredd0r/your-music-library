# ymlib — MVP plan (Stage 1)

## Context
`SPEC.md` describes a personal music library manager: download music, control which metadata fields get written, keep a clean `artist/album/track` layout, build artist playlists, and sync to external devices. A grilling session settled the design (decisions below). This plan covers **Stage 1 = feature (1)**: metadata presets plus search/download/import of tracks and albums into the library layout, and the foundations everything else needs. The repo is empty apart from `SPEC.md`; Go 1.26 is installed.

Out of scope: no lucida.to / monochrome.st (or similar unlicensed) source integration is built. Sources are external plugins; the built-in source is a local import inbox.

## Settled decisions
- **Language:** Go only. Binary `ymlib`. Test-first (TDD). No browser in core.
- **Truth:** file tags are canonical; SQLite (`modernc.org/sqlite`, no C dependency) is an index and can be rebuilt by `scan`.
- **Formats:** store whatever the source provides.
- **Fields:** Title, Album, Artist, Artists (multi-value), Album Artist, Date, Track Number, Disc Number, Genre, Picture Cover, Picture Back.
- **Presets:** user YAML at `~/.config/ymlib/config.yaml`, with built-ins `full` and `mp3-player`. A preset applies on import. `retag` shows a preview before writing.
- **Covers:** full-res `cover.jpg` (and `back.jpg`) in the album folder, plus an embedded copy sized per preset (mp3-player: ≤600px JPEG). Back cover embedded only if the preset includes it.
- **Paths:** configurable template, default `{albumartist}/{album}/{disc?}{track:02} - {title}.{ext}`. Folder = Album Artist. Names always cleaned to FAT32-safe characters.
- **Singles:** `singles: as-album | grouped`. Grouped = album "`<Artist>` Singles", track numbers by date, each file keeps its own Date, album date = earliest single, renumber/rename when a single is added. A command converts existing singles between modes.
- **Metadata providers:** whole-record fallback chain. Discogs is primary, MusicBrainz is used when Discogs finds nothing or errors. Last.fm/Spotify later, same interface. Low-confidence matches go to a **Needs Review** list; confident ones are applied automatically.
- **Duplicates:** source track ID → ISRC/MBID → normalized (albumartist, album, disc, track, title). Flags: ✓ in library, ≈ other edition.
- **Queue:** saved in SQLite, 2 parallel downloads (configurable), retry with backoff, checked in a temp folder (file decodes, duration matches) before being tagged and moved into the library.
- **Source plugins:** external executables speaking JSON over stdin/stdout (`search`, `resolve`, `download`). Built-in: local import inbox.
- **Cache:** in-memory while the app runs. Flushed to SQLite on exit, and also periodically so a crash doesn't lose everything. Loaded on start. Entries older than a configurable max age (default 6 months) are purged on startup. Cover images cached as files in `~/.cache/ymlib/`.
- **Credentials:** OS keychain (`zalando/go-keyring`), with an environment variable as an alternative.
- **Platform:** macOS + Linux behind `internal/platform` (config/cache dirs, volume detection), so Windows can be added later.
- **Existing music:** `scan` (index only, nothing moves) is separate from `organize` (retag and move, preview first).
- **TUI:** Bubble Tea. v1 screens: Search & Download, Queue, Library browser (tags and cover), Needs Review, Devices & Sync, Settings/Presets. Covers drawn with the Kitty/iTerm2 image protocol, half-block fallback (Apple Terminal).

## Package layout
```
cmd/ymlib/            main, cobra CLI
internal/config       YAML config, presets (built-ins embedded), validation
internal/store        SQLite schema/migrations: tracks, releases, queue, review, cache
internal/cache        in-memory map + SQLite persistence, flush, max-age purge
internal/tags         TagReader/TagWriter interfaces; flac + mp3 implementations
internal/pathtmpl     template rendering + FAT32 name cleaning
internal/library      scan, organize, retag (preview → apply), dedupe matching, singles modes
internal/metadata     Provider interface, discogs, musicbrainz, chain, confidence scoring
internal/source       plugin JSON protocol, exec runner, localimport source
internal/queue        persistent job queue, workers, temp folder + checks
internal/platform     OS abstraction (dirs, volumes)
internal/tui          Bubble Tea app, screens, image renderer (kitty/iterm/halfblock)
```

## Milestones
0. **Spikes** (throwaway): FLAC/MP3 tag writing incl. multi-value Artists and pictures (`go-flac` + `flacvorbis`/`flacpicture`, `bogem/id3v2`; M4A writing support TBD), and terminal image rendering in Kitty-protocol vs. half-block. Pick the libraries.
1. **Config, presets, store** with migrations. Commands: `ymlib config show`, `ymlib preset list`.
2. **Tags + path template + library:** `scan`, `organize --dry-run`, `retag --preset X --dry-run`, singles mode conversion.
3. **Metadata:** Discogs + MusicBrainz clients (rate limits: Discogs 60/min with token, MusicBrainz 1 req/s and a required User-Agent), chain, scoring, cache, Needs Review. Command: `ymlib review`.
4. **Sources + queue:** plugin protocol and fake test plugin, localimport inbox, persistent queue, dedupe flags. Commands: `ymlib search`, `ymlib download`, `ymlib import <dir>`.
5. **TUI:** Search & Download (cover, ✓/≈), Queue, Library browser, Needs Review, Settings. Devices & Sync screen comes with Stage 5.

## Later stages
2. Duplicate flags polished across sources. 3. YAML batch list (`prefer_remasters` global and per artist; ranking: match → explicit → remaster pref → quality → standard over deluxe; per-entry report). 4. Artist playlists (`Playlists/Its <Artist>.m3u8`, relative paths, order album date → disc → track, auto-regenerated, chosen artists only). 5. Device sync (volume UUID, manifest `.yml-sync.json`, per-device target folder, mode `mirror|update` limited to that folder and to files the app copied, `include_folder_covers`; runs in TUI plus `ymlib sync`). Then: background sync toggle (launchd/systemd), format conversion per device, Last.fm/Spotify providers, Claude-Code-style command bar.

## Verification
- `go test ./...`: unit tests for template/cleaning, preset filtering, dedupe tiers, singles renumbering, cache purge; recorded HTTP fixtures for Discogs/MusicBrainz (no network); a fake source plugin binary built in tests; small generated FLAC/MP3 sample files with tags read back after writing.
- Manual: `ymlib scan` on a sample folder → `ymlib organize --dry-run` shows the expected paths → apply → check tags with `ffprobe`/`metaflac`. `ymlib import` of untagged files → Needs Review entries for unclear ones. TUI run in Ghostty and Apple Terminal to check both cover renderers.

## Milestone 0 results (2026-10-13)
- **Tags:** `go-flac/go-flac/v2` + `flacvorbis/v2` + `flacpicture/v2` (FLAC) and `bogem/id3v2/v2` (MP3, ID3v2.4, UTF-8) write every field, including front and back pictures (types 3/4). `dhowden/tag` reads both back correctly. Files still decode with ffmpeg.
  - Multi-value Artists: FLAC uses repeated `ARTISTS` comments. MP3 uses `TXXX:ARTISTS` with null-separated values (the Picard convention). ffprobe shows only the first MP3 value; that's an ffmpeg display limit, not data loss.
  - M4A: no solid pure-Go writer. Use an `ffmpeg -c copy -metadata` writer behind the same `TagWriter` interface, or postpone M4A.
- **Covers in the terminal:** detection uses `TERM_PROGRAM`/`TERM`/`KITTY_WINDOW_ID`, with a `YMLIB_IMG` override. Kitty protocol (Ghostty/Kitty/WezTerm), iTerm2 inline images, and a truecolor half-block fallback (Apple Terminal) are all implemented in under 100 lines with `golang.org/x/image/draw`.
