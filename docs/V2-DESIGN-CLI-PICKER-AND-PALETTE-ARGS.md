# Design note: CLI human IDs, TTY picker fallback, and live palette argument forms

## 1. CLI `<id>` resolution and TTY picker fallback (`matomeru-next/cmd/mtm`)

Currently, noun subcommands such as `mato task star <id>` require an exact record ID. No title lookup or interactive picker exists yet.

### Target behavior (all nouns, one shared resolver)

1. **Human-friendly resolution (works in both scripts and interactive shells):**
   - Exact record ID (`0d1e6bfdede0`)
   - Unique ID prefix (`0d1e`, like git short hashes)
   - Case-insensitive exact title match (`"test matomeru v2"`)
   - Unique case-insensitive title substring (`"matomeru v2"`)
   - If multiple records match:
     - In a non-TTY (script/agent/pipe): fail clearly listing the matching IDs and titles.
     - In an interactive TTY: open the picker pre-filtered to those matching candidates.
2. **Interactive TTY picker when `<id>` is omitted:**
   - Running `mato task star` or `mato pane edit` with no `<id>` on an interactive TTY opens a lightweight Ochazuke `widgets/picker` over that noun's records so you can fuzzy-search by title/status and hit `enter`.
   - When stdin/stdout is not a TTY, omitting `<id>` exits cleanly with a usage error instead of hanging.

## 2. Command palette (`:`) with live argument form (`ochazuke-next` + `matomeru-next`)

Currently, the command palette (`modalPalette`) is a flat `widgets/picker` with no argument preview or editor.

### Target behavior (general mechanism driven by `core.CommandSpec.Params`)

Because every `core.CommandSpec` already declares its `Params []core.ParamSpec` (name, type, `Enum`, `RefKinds`, `Default`, `Required`, `Description`), this belongs in Ochazuke v2 (`widgets/palette`, composed from `widgets/picker` and `widgets/form`) so `mato` and all `o-*2` tools inherit it without per-command special cases.

1. **Always visible as soon as a command with args is highlighted:**
   - While navigating the palette picker, highlighting any command with `len(spec.Params) > 0` immediately shows a split modal: the command picker on the left (focused) and the live `widgets/form` built from `spec.Params` on the right (visible, unfocused), displaying every argument, type, default, and description.
   - Moving the picker highlight to another command immediately swaps the visible form to that command's `Params`.
2. **Seamless focus handoff and picker tools:**
   - Press `tab` (or `right` / `ctrl+l`) to focus the argument form; press `shift+tab` (or `esc` from form normal mode) to return focus to the command list.
   - Inside the argument form, every Ochazuke form control works out of the box: `i` for Readline text input, `h`/`l` for enum choices (for example `kind: task | pane | note | document | ...` on `record.new`), `space` for booleans, and `p` for `ParamRef` record pickers or `ParamPath` file/directory pickers.
   - Pressing `enter` on the picker when a command has unfilled `Required` params automatically moves focus into the argument form instead of failing; when all required params are satisfied, `enter` executes the command immediately with the form's current values.

## 3. Panes page `n` binding (`matomeru-next`)

Alongside the two upgrades above, `record.new.here` (`n`) on `1 Panes` will create a `pane` record directly instead of falling through to the global `new task` binding.

## Repo placement

- **`~/proj/ochazuke-next`:** Reusable `widgets/palette` (picker + live `widgets/form` over `core.CommandSpec.Params`, reusable by Matomeru and all v2 extensions).
- **`~/proj/matomeru-next`:**
  - Wire `widgets/palette` into `modalPalette` with dynamic noun enums on `record.new` and `ParamRef`/`ParamPath` picker callbacks.
  - Fix `n` (`record.new.here`) on `SpacePanes` to create a `pane`.
  - Add shared human `<id>` resolution + TTY picker fallback to `cmd/mtm/nouns.go`.

Queued to start right after `ochazuke-polish-worker` finishes its current run in `~/proj/ochazuke-next`.
