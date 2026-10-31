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
step vet go vet ./...
step race go test -race ./...
step stories "$make_command" story-test STORY_PACKAGES=./cmd/mtm 'STORY_TEST_FLAGS=-race -run ^TestStories'
step split "$make_command" split-check
step states "$make_command" states-test
