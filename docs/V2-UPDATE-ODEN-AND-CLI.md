# Oden v2 CLI foundation and Ochazuke/cli bridge complete

Both CLI foundation layers are built, verified with `go vet` and `-race` tests, and committed cleanly on local branch `next` (leaving all `main` branches untouched).

## 1. `oden` v2 (`~/proj/oden-next`, `1c7205a`)

Remains 100% Go standard library (`go 1.26.0`, zero external dependencies) and 100% backward compatible with all existing `oden` tests:

- **Declarative flags & parameters (`flags.go`, `help.go`, `catalogdoc.go`):** Extended `Parameter` (`Flag`, `Short`, `Default`, `Enum`, `Repeatable`), added `ParseFlags` / `(*Ctx).Parse` with `--format json|table`, `--json`, repeatable flags, `--` terminator, rich human `Flags:` help sections, and machine-readable `commands`/`command` JSON metadata.
- **Uniform output (`output.go`):** `WriteJSON`, `WriteTable`, `(*Ctx).WriteJSON`, `(*Ctx).WriteTable`.
- **Human-friendly target resolution + injectable TTY picker (`resolve.go`):** `ResolveCandidate` / `(*Ctx).Resolve` resolves exact ID -> unique ID prefix -> exact title -> unique substring -> ambiguous list (non-TTY) or `App.Picker` callback (TTY on omitted or ambiguous target).
- **Standard extension protocol (`extproto.go`):** `(*App).AddExtension(manifest, caps, queryFn)` registers `catalog`, `query`, and `capabilities --json` in one call.

## 2. Opt-in Tier 3 `ofam.dev/ochazuke/cli` (`~/proj/ochazuke-next`, `7d29578f`)

Enforced by `deps_test.go` so zero Tier 0/1/2 packages import `oden` or `cli`:

- **Interactive TTY picker bridge (`cli/picker.go`):** `DefaultPicker()` / `NewPickerFunc(theme)` connects Ochazuke's `widgets/picker` + Readline bindings directly to `oden.WithPicker(...)` (rendering to `os.Stderr` so `stdout` stays clean for piping/JSON), plus headless-testable `NewPickerModel`.
- **Standard TUI app shell (`cli/shell.go`):** `cli.NewApp(AppInfo, args)` wires `oden.WithBuiltins()`, `oden.WithPicker(DefaultPicker())`, `version`/`--version`, `smoke`/`--smoke`, `binding list|check`, `inspect actions|bindings|modes`, `config export`, optional `catalog`/`query`/`capabilities`, and unique-prefix dispatch (`bi ch`, `ins ac`) in one call.

## Verification

- `oden-next`: `gofmt`, `go vet ./...`, `go test -trimpath -p 1 -race -count=1 ./...` pass (`ofam.dev/oden`, `cmd/o`, `examples`).
- `ochazuke-next`: `gofmt`, `go vet ./...`, `go test -p 1 -race -count=1 ./...` pass across all 19 packages (including `TestDependencyBoundaries`).
