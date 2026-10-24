# Matomeru (on ochazuke)

The work-and-knowledge workspace over the TUIOS daemon, rebuilt on the ochazuke framework. Binary: `mtm`.

Read in this order:
1. `../ochazuke/AGENTS.md`, section "Building an application on ochazuke", then its invariants. The framework owns focus, modes, modals, keys, the palette and the CLI shell; this app owns domain state, persistence and the daemon client.
2. `docs/BRIEF.md`: the first slice and the order of work. Nothing outside the slice until it is done end to end.
3. `../ochazuke/examples/csv`: the reference consumer. Copy its shape (App struct, Define blocks, AddPage/AddList, handlers that return Results, SetItems after every change, `--smoke`).
4. Spec: `~/proj/znbu/docs/outline.md` and its concept outlines. Old implementation to lift from, never to port wholesale: `~/proj/matomeru-old` (store, daemon client) and `~/proj/gmtm` (daemon client, protocol notes).

Rules:
- No verb-specific branches in the shell; a feature is a noun, its verbs and rows, and handlers on App.
- No key-name switches; keys are rows in `Define` or a bindings file.
- Every commit: `gofmt -l . && go vet ./... && go test -race ./...` green, `go run ./cmd/mtm --smoke` renders, `ochazuke.ValidateBindings` in a test.
- Never bind function keys.
- Commit after every step in the brief; do not amend.

Run: `go run ./cmd/mtm` (or `--smoke`). ochazuke is a local replace on `../ochazuke`; drop the replace once ochazuke is tagged.
