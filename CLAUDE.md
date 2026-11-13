# CLAUDE.md

**ymlib** is a personal music library manager written in Go. It imports and downloads music (through source plugins: HTTP servers the user runs), writes tags according to user presets, organizes files with a path template, and has a CLI plus a Bubble Tea TUI. Planned (see MILESTONES.md): T1 makes ymlib TUI-only (the CLI is removed), T2 replaces presets with one fixed tag format, then batch downloads (S3) and device sync (S5), then `This is <Artist>` playlists (S4).

## Start here
- `MILESTONES.md`: roadmap, status of each milestone, open questions. **Read it first, and update its status table and sections when a milestone (or part of one) is finished.**
- `PLAN-stage1.md`: settled design decisions for Stage 1 (MVP).
- `SPEC.md`: the original idea.

## Commands
```sh
make build        # bin/ymlib (gitignored)
make test         # go test ./...
make install-lint # golangci-lint (version pinned in Makefile) into bin/, skipped if present
make lint         # golangci-lint run ./...
make check        # lint + test; must pass before a milestone counts as done
make fmt          # gofmt -w
go test -race ./internal/source ./internal/download ./internal/tui ./internal/cli   # concurrent code: run with -race after changes
go run ./cmd/ymlib --help
```
golangci-lint runs its standard linters plus a gofmt check, configured in `.golangci.yml`. errcheck skips only terminal output (`fmt.Fprint*`) and test files. Use `internal/cleanup` for the rest: `defer cleanup.Close(&err, c)` with a named `err` result, `defer cleanup.Rollback(&err, tx)` (ignores the no-op after `Commit`), and `cleanup.Remove`/`RemoveAll`/`RemoveIfEmpty` joined into the returned error (the download runner logs them instead). An error that is really ignored needs an explicit `_ =` with a reason. On macOS, `sed` is BSD sed (no `\b`); use `perl -pi -e` for regex edits.
For manual runs, don't touch the user's real `~/.config` or `~/.local/share`. Point `XDG_CONFIG_HOME`, `XDG_DATA_HOME` and `XDG_CACHE_HOME` at a temporary directory (the scratchpad), with a minimal `config.yaml` there such as `library_path: <tmp>/lib` and `providers: [musicbrainz]` (an empty provider list is invalid). macOS has no `timeout` command. The TUI can be smoke-tested in a pseudo-terminal; `YMLIB_IMAGES=kitty|halfblock` forces the cover renderer. The Bash sandbox blocks connections between local processes (even 127.0.0.1), so a manual run against a plugin server in another process needs the user; automated tests use `sourcetest` in-process, which works.

## Layout
```
cmd/ymlib/          main: resolves platform.Dirs, runs cli.NewRootCommand
internal/cli        cobra commands; each takes injected platform.Dirs (tests use t.TempDir)
internal/config     config.yaml: fields, built-in + user presets, validation, Marshal
internal/tags       Track model; FLAC/MP3 read/write (only managed tags); ApplyPreset, Diff
internal/pathtmpl   path template parse/render + FAT32-safe name cleaning (no internal imports)
internal/library    Scan; Plan/Apply for organize, retag, singles, identify, ingest (import/downloads); duplicates; reviews
internal/metadata   Provider interface, Discogs + MusicBrainz clients, matching/scoring, Identifier chain
internal/cache      two-level cache (memory + SQLite cache_entries) and cover file cache
internal/secrets    API tokens: OS keychain, env var override
internal/source     plugin HTTP client (health/search/resolve/download), error codes, offline detection, Registry (FromConfig)
internal/source/sourcetest  in-process fake plugin server (httptest) for tests: fixed catalog, SetOffline/SetFailOnce/SetCorrupt
internal/queue      persistent download queue (queue_jobs)
internal/download   queue runner: resolve, download, verify, source tags, ingest
internal/tui        Bubble Tea UI (ymlib with no args / ymlib tui): screens over the packages above
internal/termimg    terminal images: Kitty Unicode placeholders, half-block fallback
internal/platform   OS abstraction (XDG-style dirs on macOS/Linux; Windows later)
internal/store      SQLite (modernc.org/sqlite) + embedded migrations in migrations/NNNN_*.sql
internal/testutil   shared fixtures: tiny.flac/tiny.mp3, CopyAudio, JPEG
docs/               plugin-protocol.md: the plugin spec (HTTP API, errors, offline behaviour, Python example)
spikes/             throwaway experiments, each its own Go module; not part of the app
```
Planned packages (see MILESTONES.md): `devsync` (S5).

## Rules
- **Test-first.** Tests never use the network, the real home directory or the real keychain (`secrets.UseMockKeychain()`). HTTP providers use recorded responses in `internal/metadata/testdata` served by `httptest`; record new ones with `curl` (MusicBrainz needs a User-Agent and allows 1 request/s). Audio fixtures are generated or tiny committed files.
- **File tags are the source of truth.** SQLite is only an index and must be rebuildable with `ymlib scan`.
- **Preview before change:** commands that move, rename or re-tag files show a preview by default and act only with `--apply`. In code this is a `PlanX` → `ApplyX` pair. Never overwrite a file because of a path collision.
- **Case-insensitive file systems:** macOS, FAT and exFAT ignore letter case. Compare paths with `pathKey` (lowercase + NFC) when checking collisions.
- **Schema:** add a new migration file. Never edit an existing one.
- **Config:** unknown YAML keys and fields are errors (strict decoding). New settings get a default in `config.Default()`, a check in `Validate()`, a line in the README config example, and tests. Older sections (`queue`, `cache`, `metadata`) replace their defaults as a whole; a new section with several keys merges key by key through pointer fields, like `filePlugins`.
- **No browser in the core.** Download sources are plugins: HTTP servers the user runs; ymlib is only a client and never starts, stops or installs plugin code. The protocol is `docs/plugin-protocol.md` (keep it, `internal/source` and `sourcetest` in step). Do not build plugins or integrations for unlicensed sources (lucida.to, monochrome.st and similar); the user knows this. The repo ships no plugins and no SDK (spec only, the user's choice). Tests use `sourcetest.New(t)`.
- **Plugins in code:** registrations live in config (`plugins.registered`, edited with `config.AddPlugin`/`RemovePlugin`/`SetPluginEnabled`, which keep comments). Build the registry with `source.FromConfig`; the TUI replaces its registry on enable/disable instead of mutating plugins (a running download keeps its own). Offline or disabled plugins make jobs wait (`queue.Wait`, woken by `queue.Wake` at each run, never counted by `NextWake`) until `plugins.offline_wait_minutes`; plugin `not_found`/`permanent` errors are permanent, everything else is retried.
- **Manual runs and the keychain:** a Discogs token may be stored in the real keychain. Clearing `YMLIB_DISCOGS_TOKEN` doesn't hide it; use `providers: [musicbrainz]` in a temp config to avoid using it.
- Logic lives in `internal/*` packages. The CLI and the TUI are thin layers over it.
- TUI: work that touches disk, network or plugins runs in a `tea.Cmd` and returns a message; screens never block in `update`. Tests drive `App.Update` through the harness in `internal/tui/tui_test.go` (no real terminal), so keep tickers behind `Deps.Refresh` and `Deps.PluginRefresh` (0 in tests).
- Match the existing style: short doc comments on exported identifiers, table-driven or focused tests, errors wrapped with context.
- The user makes git commits themselves. Don't commit unless asked.
