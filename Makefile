# Builds and installs mtm with the version stamp mtm --version prints.
# VERSION defaults to the nearest tag; override with make install VERSION=v0.1.0.
VERSION ?= $(shell git describe --tags --always --dirty)
COMMIT  ?= $(shell git rev-parse HEAD)
DATE    ?= $(shell git log -1 --format=%cs)
DIRTY   ?= $(if $(shell git status --porcelain),true,false)

REFDOC  := github.com/unixsuperhero/ochazuke/refdoc
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) \
	-X $(REFDOC).Date=$(DATE) -X $(REFDOC).Dirty=$(DIRTY)

export GOWORK := off

.PHONY: build install
build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/mtm ./cmd/mtm

install:
	go install -trimpath -ldflags "$(LDFLAGS)" ./cmd/mtm
