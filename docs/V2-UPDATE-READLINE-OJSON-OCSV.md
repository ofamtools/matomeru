# v2 update: Readline everywhere, o-json2 shape conversion, and JSON closure folding

All changes below are verified with `go vet` and `-race` tests, committed locally on branch `next` across all four v2 repos, and rebuilt into `~/bin/m2m` (`mato`), `~/bin/o-json2`, and `~/bin/o-csv2`.

## 1. Readline editing across Ochazuke v2 and all three apps

### Library (`ochazuke-next` at `71a1836b`)
- Added `widgets/input/readline.go` with 14 named `input.*` actions, `ReadlineSpecs()`, `ReadlineBindings(mode, scope)`, and a reusable rune-indexed `input.Line` cursor + kill-buffer primitive:
  - Cursor movement: `ctrl+a`/`home`, `ctrl+e`/`end`, `ctrl+b`/`left`, `ctrl+f`/`right`, `alt+b`/`ctrl+left`, `alt+f`/`ctrl+right`.
  - Editing & kill ring: `backspace`/`ctrl+h`, `ctrl+d`/`delete`, `ctrl+w`/`alt+backspace`, `alt+d`, `ctrl+u`, `ctrl+k`, `ctrl+t`, `ctrl+y`.
- Updated `widgets/input`, `widgets/form`, `widgets/picker`, `widgets/pathpicker`, and `widgets/commandline` to track a real rune cursor and handle all Readline commands.
- Updated `ui/draw_content.go` so `KindForm` and `KindPicker` position the hardware bar cursor at the active `cursor` offset and scroll horizontally around it.
- All 18 packages pass `go test -p 1 -race -count=1 ./...`.

### Matomeru v2 (`matomeru-next` at `88f184d`, rebuilt as `mato`)
- Registered `input.ReadlineSpecs()`, conflict-free `input.ReadlineBindings(...)`, and one shared `readlineCommand` router across `ModeFormEdit`, `ModeFilter`, `ModePicker`, and `ModeBindingsFilter`.
- Table `/` filter renders the inline cursor at the exact edit offset while typing (`ctrl+t` stays filter-field cycle; picker `ctrl+n`/`ctrl+p`/`ctrl+j`/`ctrl+k`/`ctrl+d`/`ctrl+u` and `pathpicker` guards stay intact).
- All 14 `input.*` actions are inspectable and rebindable in `mato inspect actions`, `mato inspect bindings`, and `B` Binding Editor.

### o-csv v2 (`o-csv-next`, rebuilt as `o-csv2`)
- Wired `input.Line`, `input.ReadlineSpecs()`, and conflict-free `input.ReadlineBindings(...)` across filter (`input`), command palette (`palette`), file picker (`files`), and row editor (`form`).
- Verified `-race` tests (`8.4s`) and `o-csv2 --smoke` (19/19 checks).

## 2. o-json2 shape conversion picker and JSON closure folding (`o-json-next` at `d893f56`, rebuilt as `o-json2`)

### Four new pipeline operations with exact `jq` parity (`transform.go`, `jq_synthesis.go`)
- `wrap-object`: wraps the focused value under an object key (`{"<key>": .}` -> `jq`: `{<key>: .}`).
- `wrap-array`: wraps the focused value in a single-element array (`[.]`).
- `object-values`: extracts an object's values as an array (`need("object"; "object-values") | [.[]]`).
- `object-keys`: extracts an object's keys in document order (`need("object"; "object-keys") | keys_unsorted`).
- Verified against real `/opt/homebrew/bin/jq` in `TestNewShapeOperationsAndJQParity`.

### Convert Shape picker (`C` -> `shape.convert`, `app.go`)
- Press `C` on any focused node to pick from every conversion that fits its type:
  - **Array**: Key by field, Wrap under object key, Wrap in array.
  - **Object**: Entries array, Values array, Keys array, Wrap under object key, Wrap in array.
  - **Scalar**: Wrap under object key, Wrap in array.
- Existing `K` (`shape.keyby`) and `E` (`shape.entries`) shortcuts stay intact.

### JSON closure folding in Raw view (`rawfold.go`, `app.go`, `view.go`)
- In Raw view (`4`) Normal mode:
  - `z a` toggles fold on the enclosing `{...}` or `[...]` block.
  - `z c` folds the enclosing block; `z o` unfolds it.
  - `z M` folds all nested JSON blocks; `z R` unfolds all blocks.
- Respects JSON string literals (ignores braces/brackets inside strings), renders collapsed blocks as single summary lines (e.g. `"orders": [ … 7 lines ],`), maps the hardware cursor onto the folded view, and automatically unfolds if you enter insert mode inside a folded block.
- Also wired `input.ReadlineBindings` into `o-json2`'s pickers (`pick`, `files`), alongside the automatic Readline + `ys`/`ds`/`cs`/`S` surround support in Raw/JSON edit mode and prompts.
- Verified `-race` tests (`5.7s`) and `o-json2 --smoke` (all checks green).
