# Matomeru v2 update: live extension catalogs, async ReadCache, and lazy shadows

Committed on `matomeru-next` (`next`) and rebuilt into `~/bin/m2m` (`~/bin/mato`).

## What is live now

### 1. Live extension catalog mounting (`app/ext_catalog.go`, `app/live.go`)
- Running `R` (`extension.refresh`) in the Extensions space now probes healthy discovered tools for `TOOL catalog` manifests and mounts every declared schema directly into Matomeru's store registry via `App.MountCatalog(binary, manifest)`.
- Mounted external nouns immediately appear in `g n` (Browse Any Noun), `mato inspect schemas`, Show pages, edit forms, filters, sorts, and grouping with zero per-tool code.

### 2. Instant async `ReadCache` reads (`app/ext_catalog.go`, `app/app.go`)
- Opening an external noun in Work serves rows immediately from `data.ReadCache` with `ext.Overlay` applied, and kicks off a deduplicated background `ext.QueryExternal` fetch when the cache is empty or stale.
- Matomeru never writes back to an external tool; all queries are strictly read-only.

### 3. Lazy local shadow records (`app/ext_catalog.go`, `app/app.go`)
- Starring (`s`), archiving (`a`), snoozing (`Z`), editing (`e`), or opening the Show page (`enter`) of a cached external record automatically materializes a local shadow via `ext.Shadow`.
- Tool-owned fields are marked `ReadOnly: true`, so edits stay as Matomeru local overrides (`(local)`) while star, archive, snooze, and notes work identically to native records.

## Verification
- `gofmt`, `go vet ./...`, and `go test -p 1 -race -count=1 ./app -run TestExternalCatalogBrowseCacheAndShadow` passed (`2.65s`, testing a live fake `o-links` extension binary through `LoadCatalog` -> `MountCatalog` -> `g n` -> async `ReadCache` -> `s` star shadow -> `enter` Show page).
- Rebuilt `~/bin/m2m` (`~/bin/mato`) and verified `mato --smoke` passed (`317us` render).
