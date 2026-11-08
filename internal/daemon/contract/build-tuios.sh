#!/bin/sh
# Build tuios binaries for the daemon contract suite without touching the
# tuios checkout: git archive of each tag into OUT/src-VERSION, then go build.
# Usage: build-tuios.sh [OUT_DIR] [TAG...]   (defaults: /tmp/mtmd-0-contract, v0.10.0 v0.12.1)
# Prints MTM_CONTRACT_TUIOS=a,b on stdout. Binaries never go on PATH or ~/bin.
set -eu
OUT=${1:-/tmp/mtmd-0-contract}; [ $# -gt 0 ] && shift
TAGS=${*:-v0.10.0 v0.12.1}
REPO=${TUIOS_REPO:-$HOME/proj/tuios}
mkdir -p "$OUT/bin"
list=""
for tag in $TAGS; do
  src="$OUT/src-$tag"; bin="$OUT/bin/tuios-$tag"
  if [ ! -x "$bin" ]; then
    rm -rf "$src"; mkdir -p "$src"
    git -C "$REPO" archive "$tag" | tar -x -C "$src"
    (cd "$src" && GOWORK=off GOFLAGS=-trimpath nice -n 20 go build -o "$bin" ./cmd/tuios) >&2
  fi
  list="${list:+$list,}$bin"
done
echo "MTM_CONTRACT_TUIOS=$list"
