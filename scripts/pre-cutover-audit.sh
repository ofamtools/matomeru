#!/usr/bin/env bash
set -euo pipefail

# Pre-cutover audit across all 17 v2 target repos, binaries, DB/config, and signing identity.
# Read-only: changes nothing on disk.

REPOS=(
  oden
  ochazuke
  matomeru
  o-csv
  o-json
  o-df
  o-md
  o-cmds
  o-lane
  o-questions
  o-fleet
  o-proj
  o-gh
  o-ai
  o-git
  o-mermaid
  o-services
)

FAIL=0

echo "=== 1. Git state (v1 main vs v2 next) ==="
printf "%-14s | %-6s | %-8s | %-6s | %-8s | %s\n" "REPO" "V1_BR" "V1_TRACK" "V2_BR" "V2_DIRTY" "V2_HEAD"
for r in "${REPOS[@]}"; do
  v1="/Users/unixsuperhero/proj/$r"
  v2="/Users/unixsuperhero/proj/${r}-next"
  b1=$(git -C "$v1" branch --show-current 2>/dev/null || echo "MISSING")
  t1=$(git -C "$v1" status --short --untracked-files=no 2>/dev/null | wc -l | tr -d ' ')
  b2=$(git -C "$v2" branch --show-current 2>/dev/null || echo "MISSING")
  d2=$(git -C "$v2" status --short 2>/dev/null | wc -l | tr -d ' ')
  h2=$(git -C "$v2" rev-parse --short HEAD 2>/dev/null || echo "NONE")
  printf "%-14s | %-6s | %-8s | %-6s | %-8s | %s\n" "$r" "$b1" "$t1" "$b2" "$d2" "$h2"
  if [ "$b1" != "main" ] || [ "$t1" != "0" ] || [ "$b2" != "next" ] || [ "$d2" != "0" ]; then
    FAIL=1
  fi
done

echo ""
echo "=== 2. Local replace directives in *-next/go.mod ==="
grep -H "replace ofam.dev/" /Users/unixsuperhero/proj/*-next/go.mod || true

echo ""
echo "=== 3. Matomeru DB & config directories ==="
ls -ld ~/.local/share/matomeru ~/.local/share/matomeru-next ~/.config/matomeru ~/.config/matomeru-next 2>&1 || true

echo ""
echo "=== 4. Apple Developer ID codesigning identity ==="
if security find-identity -v -p codesigning | grep -q "Developer ID Application"; then
  security find-identity -v -p codesigning | grep "Developer ID Application"
else
  echo "MISSING Developer ID Application signing identity!"
  FAIL=1
fi

echo ""
if [ "$FAIL" -eq 0 ]; then
  echo "AUDIT PASS: all 17 repos clean and ready for cutover."
else
  echo "AUDIT WARN/FAIL: review issues above before cutting over."
  exit 1
fi
