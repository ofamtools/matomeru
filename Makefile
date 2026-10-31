# Builds and installs mtm with the version stamp mtm --version prints.
# VERSION defaults to the nearest tag; override with make install VERSION=v0.1.0.
VERSION ?= $(shell git describe --tags --always --dirty)
COMMIT  ?= $(shell git rev-parse HEAD)
DATE    ?= $(shell git log -1 --format=%cs)
DIRTY   ?= $(if $(shell git status --porcelain),true,false)

REFDOC  := ofam.dev/ochazuke/refdoc
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) \
	-X $(REFDOC).Date=$(DATE) -X $(REFDOC).Dirty=$(DIRTY)

export GOWORK := off

.PHONY: build install gate story-bin story-test split-check
build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/mtm ./cmd/mtm

install:
	go install -trimpath -ldflags "$(LDFLAGS)" ./cmd/mtm

# One release gate, with elapsed seconds per step.
gate:
	+@sh scripts/gate.sh "$(MAKE)"

# The story tester (-tags monogatari): mtm story run|open|list|explore|...
# as bin/mtm-story, never installed over mtm. See README "Stories".
STORY_BIN ?= bin/mtm-story
story-bin:
	go build -trimpath -tags monogatari -o $(STORY_BIN) ./cmd/mtm

# The standalone tagged vet/race suite includes all tests and stories.
# make gate narrows the tagged run to TestStories after the untagged race pass.
STORY_PACKAGES ?= ./...
STORY_TEST_FLAGS ?= -race
story-test:
	go vet -tags monogatari $(STORY_PACKAGES)
	go test -trimpath $(STORY_TEST_FLAGS) -tags monogatari $(STORY_PACKAGES)

# A normal build links no monogatari; the tester does.
split-check:
	@mkdir -p bin
	go build -trimpath -o bin/mtm ./cmd/mtm
	@n=$$(go tool nm bin/mtm | grep -c monogatari); echo "mtm: $$n monogatari symbols"; test "$$n" -eq 0
	$(MAKE) story-bin STORY_BIN=bin/mtm-story
	@n=$$(go tool nm bin/mtm-story | grep -c monogatari); echo "mtm-story: $$n monogatari symbols"; test "$$n" -gt 0

# Portable state/key reference; reads committed data and never runs the app.
.PHONY: states-test states-export
states-test:
	python3 -I scripts/states_export_test.py

states-export:
	@test -n "$(OUT)" || { echo 'give OUT=/path/to/export' >&2; exit 2; }
	python3 -I scripts/states_export.py docs/keys/states.json "$(OUT)"
