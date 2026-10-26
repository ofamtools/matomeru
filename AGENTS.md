# Matomeru (on ochazuke)

The work-and-knowledge workspace over the TUIOS daemon, rebuilt on the ochazuke framework. Binary: `mtm`.

Plans and todos: family plans, todos and the roadmap live in `~/hub/o-plans/` (`README.md` is the index). Read it before planning work; add or update items there. Do not add a TODO.md or docs/plans/ to this repo.

Read in this order:
1. `../ochazuke/AGENTS.md`, section "Building an application on ochazuke", then its invariants. The framework owns focus, modes, modals, keys, the palette and the CLI shell; this app owns domain state, persistence and the daemon client.
2. `docs/BRIEF.md`: the first slice. Nothing outside the slice until it is done end to end. Open work and its order are in `~/hub/o-plans/`.
3. `../ochazuke/examples/csv`: the reference consumer. Copy its shape (App struct, Define blocks, AddPage/AddList, handlers that return Results, SetItems after every change, `--smoke`).
4. Spec: `~/proj/znbu/docs/outline.md` and its concept outlines. Old implementation to lift from, never to port wholesale: `~/proj/matomeru-old` (store, daemon client) and `~/proj/gmtm` (daemon client, protocol notes).

Rules:
- No verb-specific branches in the shell; a feature is a noun, its verbs and rows, and handlers on App.
- No key-name switches; keys are rows in `Define` or a bindings file.
- Every commit: `gofmt -l . && GOWORK=off go vet ./... && GOWORK=off go test -race ./...` green, `GOWORK=off go run ./cmd/mtm --smoke` renders, `ochazuke.ValidateBindings` in a test. `--smoke` renders a fresh temp database unless `--data` is given explicitly and never reaches the daemon (`smokeOptions`); with real data, point `--data` at a copy in a temp dir (`--data <tmp>/mtm.db --smoke`). Never run the TUI or a CLI command without `--data` at a temp dir: the default is the owner's real database.
- Never bind function keys.
- Commit after every step in the brief; do not amend.

New repositories are made with `o proj new` (`~/proj/o-proj`, its own repo); `mtm proj` is mtm's own `project` noun (work context), not that tool.

Run: `go run ./cmd/mtm` (or `--smoke`, which is safe anywhere). oden and ochazuke are required at their tags (`v0.1.0`); an untracked `go.work` (`go work init . && go work use ../oden ../ochazuke`) builds against the sibling checkouts, and `GOWORK=off` builds from the tags as a fresh clone does.
