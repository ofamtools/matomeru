#!/bin/sh
# Run from the repository root (make gate). Stop at the first failure,
# reporting that step's elapsed seconds as well as successful steps.
set -eu
export GOWORK=off
export GOFLAGS="-trimpath ${GOFLAGS:-}"
make_command=${1:-make}

step() {
    label=$1
    shift
    started=$(date +%s)
    result=0
    "$@" || result=$?
    finished=$(date +%s)
    printf '%s: %s seconds (exit %s)\n' "$label" "$((finished-started))" "$result"
    return "$result"
}

format_check() {
    unformatted=$(gofmt -l .) || return "$?"
    if [ -n "$unformatted" ]; then
        printf '%s\n' "$unformatted" >&2
        return 1
    fi
}

step gofmt format_check
# Vet once per build-tag set. ochazuke_strict leaves out ochazuke's deprecated
# names, so a use fails the vet (this replaces both the plain vet and the old
# TestNoDeprecatedOchazuke); monogatari adds the story files (story-vet).
step vet go vet -tags ochazuke_strict ./...
step vet-tagged "$make_command" story-vet STORY_PACKAGES=./cmd/mtm
# -count=1: the gate never reuses a cached result, and the test cache costs
# minutes of hashing the test's file and env reads after the last test.
step race go test -race -count=1 ./...
# Stories replay key flows on one App; the race step covers concurrency.
step stories "$make_command" story-run STORY_PACKAGES=./cmd/mtm 'STORY_TEST_FLAGS=-count=1 -run ^TestStories'
step split "$make_command" split-check
step states "$make_command" states-test
