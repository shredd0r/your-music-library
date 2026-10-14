# CLAUDE.md

**ymlib** is a personal music library manager written in Go. It imports and downloads music, writes tags according to user presets, organizes files with a path template, builds artist playlists, and syncs to external devices. There is a CLI now; a Bubble Tea TUI comes later.

## Start here
- `MILESTONES.md`: roadmap, status of each milestone, open questions. **Read it first, and update its status table and sections when a milestone (or part of one) is finished.**
- `PLAN-stage1.md`: settled design decisions for Stage 1 (MVP).
- `ANSWERS.md`: the user's raw answers from the design session; check it before re-asking a settled question.
- `SPEC.md`: the original idea.

## Commands
```sh
make build        # bin/ymlib (gitignored)
make test         # go test ./...
make lint         # gofmt check + go vet + staticcheck
make check        # lint + test; must pass before a milestone counts as done
make fmt          # gofmt -w
go run ./cmd/ymlib --help
```
staticcheck is pinned as a Go tool dependency in `go.mod` (`go tool staticcheck`), so there's nothing to install. On macOS, `sed` is BSD sed (no `\b`); use `perl -pi -e` for regex edits.
For manual runs, don't touch the user's real `~/.config` or `~/.local/share`. Point `XDG_CONFIG_HOME`, `XDG_DATA_HOME` and `XDG_CACHE_HOME` at a temporary directory.

## Layout
```
cmd/ymlib/          main: resolves platform.Dirs, runs cli.NewRootCommand
internal/cli        cobra commands; each takes injected platform.Dirs (tests use t.TempDir)
internal/config     config.yaml: fields, built-in + user presets, validation, Marshal
internal/tags       Track model; FLAC/MP3 read/write (only managed tags); ApplyPreset, Diff
internal/pathtmpl   path template parse/render + FAT32-safe name cleaning (no internal imports)
internal/library    Scan, Plan/Apply for organize, retag, singles; index rows (Entry)
internal/platform   OS abstraction (XDG-style dirs on macOS/Linux; Windows later)
internal/store      SQLite (modernc.org/sqlite) + embedded migrations in migrations/NNNN_*.sql
internal/testutil   shared fixtures: tiny.flac/tiny.mp3, CopyAudio, JPEG
spikes/             throwaway experiments, each its own Go module; not part of the app
```
Planned packages (see MILESTONES.md): `metadata`, `cache`, `source`, `queue`, `tui`.

## Rules
- **Test-first.** Tests never use the network or the real home directory. HTTP providers use recorded fixtures (`httptest`); audio fixtures are generated or tiny committed files.
- **File tags are the source of truth.** SQLite is only an index and must be rebuildable with `ymlib scan`.
- **Preview before change:** commands that move, rename or re-tag files show a preview by default and act only with `--apply`. In code this is a `PlanX` → `ApplyX` pair. Never overwrite a file because of a path collision.
- **Case-insensitive file systems:** macOS, FAT and exFAT ignore letter case. Compare paths with `pathKey` (lowercase + NFC) when checking collisions.
- **Schema:** add a new migration file. Never edit an existing one.
- **Config:** unknown YAML keys and fields are errors (strict decoding). New settings get a default in `config.Default()`, a check in `Validate()`, and tests.
- **No browser in the core.** Download sources are external plugins (JSON over stdin/stdout). Do not build integrations for unlicensed sources (lucida.to, monochrome.st and similar); the user knows this.
- Logic lives in `internal/*` packages. The CLI and the future TUI are thin layers over it.
- Match the existing style: short doc comments on exported identifiers, table-driven or focused tests, errors wrapped with context.
- The user makes git commits themselves. Don't commit unless asked.
