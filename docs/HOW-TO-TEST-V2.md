# How to test the latest Matomeru v2 and v2 extensions

Everything runs side-by-side in `~/bin/` without touching your v1 `mtm` binary, v1 `o-*` binaries, or v1 `~/.local/share/matomeru/mtm.db`.

## 1. Run Matomeru v2 (`mato` or `m2m`)

`mato` is a symlink to `~/bin/m2m`. It stores its own v2 database in `~/.local/share/matomeru-next/mtm.db` and its own config in `~/.config/matomeru-next/`.

```sh
# Interactive guided tutorial (pure in-memory, touches zero files)
mato --tutorial

# Open your real v2 workspace (persistent ~/.local/share/matomeru-next/mtm.db)
mato

# Try a fresh seeded demo workspace in a temp DB (safe sandbox)
mato --db /tmp/mato-demo.db --seed-demo

# Preview migrating your real v1 mtm.db (read-only dry run; writes nothing)
mato migrate --from ~/.local/share/matomeru/mtm.db --dry-run

# Apply the v1 migration into ~/.local/share/matomeru-next/mtm.db (v1 DB stays untouched)
mato migrate --from ~/.local/share/matomeru/mtm.db --apply

# Run the headless end-to-end smoke suite
mato --smoke
```

## 2. Highlights to try inside `mato`

- **Empty section focus + add:** Open any task (`enter`), `j`/`k` into an empty Show section like `Schedules`, and press `n` (`record.new.here`) to create a schedule pre-linked to that task.
- **Focused-record actions picker:** Press `;` on any record to see only the verbs valid for that record right now.
- **Readline editing everywhere:** Press `/`, `e` then `i`, `:`, or `!` and use `ctrl+a`/`ctrl+e`, `ctrl+w`, `ctrl+u`, `ctrl+k`, `alt+b`/`alt+f`, `ctrl+y`.
- **Shell I/O redirection:** Press `!` (shell command with `$REF`, `$ID`, `$TITLE`, `$FILE`, `$DIR`), `|` (pipe focused record JSON into a command, stdout goes to register `""`), or `g !` (`r!` capture command output into a new note). Press `ctrl+r` then a register name inside the prompt to paste registers.
- **Inspect live registries:** Press `B` (live binding editor), `g n` (pick any noun, Actions, or Modes), or run from CLI:
  ```sh
  mato inspect actions
  mato inspect bindings
  mato inspect modes
  mato inspect schemas
  ```
- **Live extension catalog mounting:** Go to space `4` (`Extensions`), press `R` to discover extensions, then `g n` to browse external nouns mounted from v2 tools.

## 3. Test the seven migrated v2 extensions

All seven read your existing v1 data files without breaking v1:

```sh
o-df2          # Disk usage explorer (or: o-df2 --smoke, o-df2 catalog)
o-md2          # Markdown workspace viewer/editor (or: o-md2 --smoke)
o-cmds2        # Command runner & job history (or: o-cmds2 --smoke)
o-lane2        # Lane & plan tracker (or: o-lane2 --smoke)
o-questions2   # Questions & decisions tracker (or: o-questions2 --smoke)
o-json2        # JSON explorer, Raw folding (za/zM/zR), Convert Shape (C)
o-csv2         # CSV table explorer with Readline everywhere
```

## Note on Wave C build

`mtm-wavec-worker` has written the Wave C features (full noun CLI parity, Data-Flow Encapsulation, `g r` Recents, Outline `J`/`K`/`>`/`<`/`space`/`i`, and `o-*2` discovery) in `~/proj/matomeru-next` and was just nudged to finish its test run and rebuild `~/bin/m2m`. Right now `mato` runs the latest pre-Wave-C build (`b72dc4e`); as soon as `mtm-wavec-worker` finishes its build, `mato` will automatically pick up Wave C too.
