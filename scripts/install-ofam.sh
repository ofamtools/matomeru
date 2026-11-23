#!/usr/bin/env bash
set -euo pipefail

# Standalone OFAM new-machine bootstrapper.
# Installs core OFAM Go binaries into ~/bin (or $GOBIN) and configures agent
# instructions, shared skills, and Claude Code hooks via `mtm setup machine`.

DRY_RUN=0
SKIP_SETUP=0
SETUP_ONLY=0
WITH_SIBLINGS=0
FORCE=0
TARGET_HOME=""

usage() {
  cat <<'EOF'
Usage: install-ofam.sh [options]

Install core OFAM binaries into ~/bin (or $GOBIN) and run `mtm setup machine`.

Options:
  --home DIR        Override target HOME directory passed to `mtm setup`
  --dry-run         Print planned commands and run `mtm setup machine --dry-run`
  --skip-setup      Install binaries only; do not run `mtm setup machine`
  --setup-only      Skip `go install`; run `mtm setup machine` using installed `mtm`
  --with-siblings   Also attempt to install optional sibling `o-*` tools
  --force           Forward `--force` to `mtm setup machine`
  -h, --help        Show this help message
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    --skip-setup)
      SKIP_SETUP=1
      shift
      ;;
    --setup-only)
      SETUP_ONLY=1
      shift
      ;;
    --with-siblings)
      WITH_SIBLINGS=1
      shift
      ;;
    --force)
      FORCE=1
      shift
      ;;
    --home)
      if [[ $# -lt 2 ]]; then
        echo "install-ofam.sh: --home requires a directory" >&2
        exit 2
      fi
      TARGET_HOME="$2"
      shift 2
      ;;
    --home=*)
      TARGET_HOME="${1#--home=}"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "install-ofam.sh: unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ "$SKIP_SETUP" -eq 1 && "$SETUP_ONLY" -eq 1 ]]; then
  echo "install-ofam.sh: --skip-setup and --setup-only cannot both be set" >&2
  exit 2
fi

EFFECTIVE_HOME="${TARGET_HOME:-$HOME}"
INSTALL_BIN="${GOBIN:-$EFFECTIVE_HOME/bin}"

CORE_PACKAGES=(
  "ofam.dev/oden/cmd/o@v1.1.0"
  "ofam.dev/o-df@v1.2.0"
  "ofam.dev/matomeru/cmd/mtm@latest"
  "ofam.dev/matomeru/cmd/mtm-hook@latest"
)

SIBLING_PACKAGES=(
  "ofam.dev/o-lane@latest"
  "ofam.dev/o-questions@latest"
  "ofam.dev/o-cmds@latest"
  "ofam.dev/o-md@latest"
  "ofam.dev/o-git@latest"
  "ofam.dev/o-proj@latest"
  "ofam.dev/o-ai@latest"
  "ofam.dev/o-gh@latest"
  "ofam.dev/o-csv@latest"
  "ofam.dev/o-json@latest"
  "ofam.dev/o-mermaid@latest"
  "ofam.dev/o-fleet@latest"
  "ofam.dev/o-services@latest"
  "ofam.dev/nabe@latest"
)

run_cmd() {
  if [[ "$DRY_RUN" -eq 1 ]]; then
    printf '[dry-run] %s\n' "$*"
    return 0
  fi
  "$@"
}

if [[ "$SETUP_ONLY" -eq 0 ]]; then
  if ! command -v go >/dev/null 2>&1; then
    echo "install-ofam.sh: Go toolchain not found in PATH" >&2
    exit 1
  fi

  run_cmd mkdir -p "$INSTALL_BIN"

  echo "Installing core OFAM binaries into $INSTALL_BIN..."
  for pkg in "${CORE_PACKAGES[@]}"; do
    echo "  go install $pkg"
    run_cmd env GOWORK=off GOFLAGS=-trimpath GOBIN="$INSTALL_BIN" go install "$pkg"
  done

  if [[ "$WITH_SIBLINGS" -eq 1 ]]; then
    echo "Installing optional sibling o-* tools into $INSTALL_BIN..."
    for pkg in "${SIBLING_PACKAGES[@]}"; do
      echo "  go install $pkg"
      if ! run_cmd env GOWORK=off GOFLAGS=-trimpath GOBIN="$INSTALL_BIN" go install "$pkg"; then
        echo "  warning: optional package failed to install: $pkg" >&2
      fi
    done
  fi
fi

if [[ "$SKIP_SETUP" -eq 1 ]]; then
  echo "Skipping machine setup (--skip-setup)."
  exit 0
fi

MTM_BIN="$INSTALL_BIN/mtm"
if [[ ! -x "$MTM_BIN" ]]; then
  if command -v mtm >/dev/null 2>&1; then
    MTM_BIN="$(command -v mtm)"
  fi
fi

SETUP_ARGS=(setup machine)
if [[ -n "$TARGET_HOME" ]]; then
  SETUP_ARGS+=(--home "$TARGET_HOME")
fi
if [[ "$DRY_RUN" -eq 1 ]]; then
  SETUP_ARGS+=(--dry-run)
fi
if [[ "$FORCE" -eq 1 ]]; then
  SETUP_ARGS+=(--force)
fi

if [[ "$DRY_RUN" -eq 1 && "$SETUP_ONLY" -eq 0 ]]; then
  printf '[dry-run] %s %s\n' "$MTM_BIN" "${SETUP_ARGS[*]}"
  exit 0
fi

if [[ ! -x "$MTM_BIN" ]]; then
  if [[ "$DRY_RUN" -eq 1 ]]; then
    printf '[dry-run] %s %s\n' "$MTM_BIN" "${SETUP_ARGS[*]}"
    exit 0
  fi
  echo "install-ofam.sh: mtm binary not found at $MTM_BIN or in PATH" >&2
  exit 1
fi

echo "Running $MTM_BIN ${SETUP_ARGS[*]}..."
"$MTM_BIN" "${SETUP_ARGS[@]}"
