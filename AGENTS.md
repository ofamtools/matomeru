# Matomeru (on ochazuke)

The work-and-knowledge workspace over the TUIOS daemon, rebuilt on the ochazuke framework. Binary: `mtm`.

Keep planning work in the family planning workspace, separate from this repository. Do not add a TODO.md or docs/plans/ to this repo.

Read in this order:
1. `../ochazuke/AGENTS.md`, section "Building an application on ochazuke", then its invariants. The framework owns focus, modes, modals, keys, the palette and the CLI shell; this app owns domain state, persistence and the daemon client.
2. `docs/BRIEF.md`: the first slice. Nothing outside the slice until it is done end to end. Follow the agreed work order for subsequent features.
3. `../ochazuke/examples/csv`: the reference consumer. Copy its shape (App struct, Define blocks, AddPage/AddList, handlers that return Results, SetItems after every change, `--smoke`).
4. Spec: `~/proj/znbu/docs/outline.md` and its concept outlines. Old implementation to lift from, never to port wholesale: `~/proj/matomeru-old` (store, daemon client) and `~/proj/gmtm` (daemon client, protocol notes).

Rules:
- No verb-specific branches in the shell; a feature is a noun, its verbs and rows, and handlers on App.
- No key-name switches; keys are rows in `Define` or a bindings file.
- Every commit: `make gate` green (gofmt check, untagged vet and race suite, tagged `TestStories` only, split-check, states-test; seconds printed per step), `GOWORK=off go run ./cmd/mtm --smoke` renders, `ochazuke.ValidateBindings` in a test. `--smoke` renders a fresh temp database unless `--data` is given explicitly and never reaches the daemon (`smokeOptions`); with real data, point `--data` at a copy in a temp dir (`--data <tmp>/mtm.db --smoke`). Never run the TUI or a CLI command without `--data` at a temp dir: the default is the owner's real database.
- Tests in `cmd/mtm` begin with `t.Parallel()` or have a `// serial: <reason>` doc comment for unavoidable process-wide state. `TestParallelTests` checks all test files, including tagged ones. Re-apply the default with the idempotent `scripts/parallel_tests.py` after merges; use app environment/clock seams instead of process mutations.
- Never bind function keys.
- The story tester is the `monogatari` build tag (`cmd/mtm/story.go`, `storyfakes.go`, `stories_test.go`; `story_off.go` is the normal build). A change to those files or to `cmd/mtm/stories/` also runs `make story-test` and `make split-check` (the normal `mtm` links 0 monogatari symbols). Stories never touch the owner's data: every session is a temp database and a dead socket (README "Stories").
- Commit after every step in the brief; do not amend.

New repositories are made with `o proj new` (`~/proj/o-proj`, its own repo); `mtm proj` is mtm's own `project` noun (work context), not that tool.

Run: `go run ./cmd/mtm` (or `--smoke`, which is safe anywhere). mtm requires ochazuke (`v0.11.0`) and oden `v0.1.1` at their tags; an untracked `go.work` (`go work init . && go work use ../oden ../ochazuke`) builds against the sibling checkouts, and `GOWORK=off` builds from the tags as a fresh clone does.
