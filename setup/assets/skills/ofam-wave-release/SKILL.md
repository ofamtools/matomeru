---
name: ofam-wave-release
description: End-of-wave release runbook for o-family Go repositories and binaries. Use when closing a wave, tagging a release, or building, Apple Developer ID signing, and atomically installing o-family binaries into ~/bin/.
---

# o-family Wave Release Runbook

Use this runbook when closing a wave or shipping updated o-family Go binaries (`mtm`, `nabe`, `o-lane`, `o-questions`, `o-cmds`, `o-md`, `o-git`, and sibling tools). Execute steps in order and stop immediately if any check fails.

**Fast path:** Use `o-release <repo> [patch|minor|major|vX.Y.Z]` (`~/hub/tools/o-release`, installed at `~/bin/o-release`). Pass `--skip-tests` when free public GitHub Actions CI is already green on the commit being released.

## 1. Verify repository health and run checks

1. Confirm `go.mod` has no stray local `replace` directives pointing at sibling checkouts or worktrees:

   ```bash
   rg -n '^\s*replace\b' go.mod || true
   ```

2. Run Go checks one at a time through `~/bin/heavy` (never parallel local builds or test runs) with `GOWORK=off GOFLAGS=-trimpath`:

   ```bash
   git ls-files '*.go' | xargs -r gofmt -l
   heavy env GOWORK=off GOFLAGS=-trimpath go vet ./...
   heavy env GOWORK=off GOFLAGS=-trimpath go test -race ./...
   ```

   `gofmt -l` must print nothing. For configuration-only changes, scale tests per the "Checks scale with the change" rule.

3. For TUI binaries (`mtm`), verify `--smoke` and `binding check` against isolated temporary directories (never the owner's live data or config):

   ```bash
   tmp="$(mktemp -d /tmp/ofam-release-check.XXXXXX)"
   trap 'rm -rf "$tmp"' EXIT
   HOME="$tmp/home" XDG_CONFIG_HOME="$tmp/cfg" XDG_STATE_HOME="$tmp/state" XDG_DATA_HOME="$tmp/data" \
     "$bin_path" --smoke
   HOME="$tmp/home" XDG_CONFIG_HOME="$tmp/cfg" XDG_STATE_HOME="$tmp/state" XDG_DATA_HOME="$tmp/data" \
     "$bin_path" binding check
   ```

## 2. Commit with explicit paths

1. Inspect `git status -sb` and `git diff --stat`.
2. Stage only explicit paths (`git add path/one path/two`). Never run `git add -A` or `git add -u` across dirty repos such as `~/hub`.
3. Commit cleanly with a concise message (and required trailers when applicable).
4. In `~/hub`, always let `.githooks/pre-commit` (`o-dotfiles audit --staged`) run. Never pass `--no-verify`, and **never push `~/hub`**.

## 3. Tag, push, and clean merged worktrees

1. Check existing semver tags and choose the next `vX.Y.Z`:

   ```bash
   git tag --sort=-v:refname | head -n 5
   ```

2. Create an annotated semver tag and push `main` plus the tag (code repos only, never `~/hub`):

   ```bash
   git tag -a "vX.Y.Z" -m "vX.Y.Z: <concise summary>"
   git push origin main "vX.Y.Z"
   ```

3. Remove merged lane worktrees and delete their local branches immediately once confirmed in `origin/main`:

   ```bash
   git fetch origin
   git merge-base --is-ancestor "<branch>" origin/main
   ```

   Never remove a worktree with uncommitted changes or a branch not present in `origin/main`; list any remaining ones instead.

## 4. Build, Apple Developer ID sign, and atomically install to `~/bin/`

1. Locate the installed Apple Developer ID signing identity:

   ```bash
   CODESIGN_ID="$(security find-identity -v -p codesigning | awk -F'"' '/Developer ID Application:/ {print $2; exit}')"
   test -n "$CODESIGN_ID"
   ```

2. Build each binary to a staged path inside `~/bin/` so the final swap is atomic on the same filesystem:

   ```bash
   stage="$HOME/bin/.${bin}.new.$$"
   heavy env GOWORK=off GOFLAGS=-trimpath go build -o "$stage" ./cmd/"$bin"
   ```

   Include the repo's version `-ldflags` when the repo embeds version strings at build time.

3. Sign with hardened runtime and secure timestamp, then verify strictly before moving into place:

   ```bash
   codesign --force --options runtime --timestamp --sign "$CODESIGN_ID" "$stage"
   codesign --verify --deep --strict --verbose=2 "$stage"
   ```

4. Swap atomically and verify the installed binary version:

   ```bash
   mv -f "$stage" "$HOME/bin/$bin"
   "$HOME/bin/$bin" version || "$HOME/bin/$bin" --version
   ```

5. If `mtm` was installed, upgrade the daemon in place without killing live panes and let the owner reload the TUI with `g U`:

   ```bash
   "$HOME/bin/mtm" daemon upgrade --json
   ```

## 5. Update lanes, dossiers, and report

1. Update each affected lane's status (`merged` or `done`) and update its linked lane dossier (Markdown doc) with the commit SHA, tag, checks, and installed binary version.
2. Deliver a concise release report containing:
   - Commit SHA(s) and annotated tag(s)
   - Verification commands run (`gofmt`, `go vet`, `go test -race`, `--smoke`, `binding check`)
   - Installed binary path(s), version output, and `codesign --verify` status
   - Removed worktrees (or remaining unmerged ones)
   - Updated lane IDs, statuses, and dossier links
