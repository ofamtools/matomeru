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

.PHONY: build install gate story-bin story-vet story-run story-test split-check
build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/mtm ./cmd/mtm
	go build -trimpath -ldflags "-s -w" -o bin/mtm-hook ./cmd/mtm-hook

# mtm-hook goes beside mtm: a launch names it for hooks when it is there.
install:
	go install -trimpath -ldflags "$(LDFLAGS)" ./cmd/mtm
	go install -trimpath -ldflags "-s -w" ./cmd/mtm-hook

# One release gate, with elapsed seconds per step.
gate:
	+@sh scripts/gate.sh "$(MAKE)"

# The story tester (-tags monogatari): mtm story run|open|list|explore|...
# as bin/mtm-story, never installed over mtm. See README "Stories".
STORY_BIN ?= bin/mtm-story
story-bin:
	go build -trimpath -tags monogatari -o $(STORY_BIN) ./cmd/mtm

# Run only the area walkthroughs; the shipping build owns the race pass.
# Tagged strict vet stays available through story-vet and story-test.
STORY_PACKAGES ?= ./cmd/mtm
STORY_TEST_FLAGS ?= -run '^TestStories$$' -count=1
story-vet:
	go vet -tags monogatari,ochazuke_strict $(STORY_PACKAGES)
story-run:
	go test -trimpath $(STORY_TEST_FLAGS) -tags monogatari $(STORY_PACKAGES)
story-test: story-vet story-run

# A normal build links no monogatari; the tester does.
split-check:
	@mkdir -p bin
	go build -trimpath -o bin/mtm ./cmd/mtm
	@n=$$(go tool nm bin/mtm | grep -c monogatari); echo "mtm: $$n monogatari symbols"; test "$$n" -eq 0
	$(MAKE) story-bin STORY_BIN=bin/mtm-story
	@n=$$(go tool nm bin/mtm-story | grep -c monogatari); echo "mtm-story: $$n monogatari symbols"; test "$$n" -gt 0

# Third-party notices embedded in mtm (internal/notices/data; mtm license all|show).
# licenses regenerates them from go list -deps and the module cache after a go.mod
# change; licenses-check fails when the files on disk differ. TestEmbeddedDataIsCurrent
# is the fast check (go.mod against the data, unknown or disallowed licences).
.PHONY: licenses licenses-check
licenses:
	go run ./internal/notices/gen
licenses-check:
	go run ./internal/notices/gen -check

# Portable state/key reference; reads committed data and never runs the app.
.PHONY: states-test states-export
states-test:
	python3 -I scripts/states_export_test.py

states-export:
	@test -n "$(OUT)" || { echo 'give OUT=/path/to/export' >&2; exit 2; }
	python3 -I scripts/states_export.py docs/keys/states.json "$(OUT)"
