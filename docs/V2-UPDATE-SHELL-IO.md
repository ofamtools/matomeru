# Matomeru v2 update: shell I/O redirection (`!`, `|`, `r!`)

Committed on `matomeru-next` (`next`) and rebuilt into `~/bin/m2m` (`~/bin/mato`).

## What is live now

### Shell I/O command bar (`app/cmdline.go`, `app/bindings.go`, `app/app.go`, `app/view.go`)
Backed by Ochazuke's `widgets/commandline` with full Readline editing, per-prefix history (`up`/`down`/`ctrl+p`/`ctrl+n`), and `ctrl+r <reg>` register paste (`""`, `"0`, `"%`, `"a`-`"z`, `"+`, `":`, `"/`):

- **`!` (`shell.open` with prefix `!`)**: Runs a shell command as a `run` record in the focused record's working directory (`defaultDir` / `cwd`), expanding `$REF`, `$ID`, `$TITLE`, `$FILE`, and `$DIR` shell-safely from the focused record.
- **`|` (`shell.open` with prefix `|`)**: Pipes the focused record (its `body` if present, otherwise pretty JSON of its merged fields) into a shell command off the UI goroutine, records stdout and exit code as a `run` linked to the task (when focused on a task), and yanks stdout into the unnamed register.
- **`g !` (`shell.open` with prefix `r!`)**: Runs a shell command off the UI goroutine, captures stdout into a new `note` linked via `about` to the focused record, and yanks stdout into the unnamed register.
- Syncs `"%` (`core.RegPath`) right before opening the bar so `ctrl+r %` always pastes the currently focused record's file path or ref.

## Verification
- `gofmt`, `go vet ./...`, and `go test -p 1 -race -count=1 . ./app -run "TestDependencyBoundaries|TestShellIORedirectionCommandLine|TestInspect"` passed (`5.79s`).
- Rebuilt `~/bin/m2m` (`~/bin/mato`) and verified `mato --smoke` passed (`286us` render).

## Current v2 stack snapshot (all clean on local branch `next`, unpushed)
- `ochazuke-next` (`71a1836b`): Readline actions, default bindings, and cursor editing across all input widgets (18/18 packages `-race`).
- `o-json-next` (`d893f56` -> `~/bin/o-json2`): Convert Shape picker (`C`) with `wrap-object`, `wrap-array`, `object-values`, `object-keys` + real `jq` parity, JSON closure folding (`z a`/`z c`/`z o`/`z M`/`z R`), surround + Readline in Raw view/prompts/pickers.
- `o-csv-next` (`9c728da` -> `~/bin/o-csv2`): Readline editing across filter, palette, file picker, and row form (19/19 smoke).
- `matomeru-next` (`~/bin/m2m` / `~/bin/mato`): Batch 2 daily driver, focused-record actions picker (`;`), focusable empty Show-page sections, tidy breadcrumbs, Readline across all 5 input modes, live extension catalog mounting + async `ReadCache` + lazy shadows, and `!` / `|` / `r!` shell I/O redirection.
