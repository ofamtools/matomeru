package main

import (
	"fmt"
	"io"

	"github.com/unixsuperhero/ochazuke/refdoc"
)

// Build stamps set with the linker (make install, docs/release-notes):
//
//	go build -ldflags "-X main.version=v0.1.0 -X main.commit=$(git rev-parse HEAD)" ./cmd/mtm
//
// Empty, the build info the go command embeds is used instead.
var (
	version string
	commit  string
)

// init lays the linker stamps over ochazuke's, so the docs viewer's
// header names the same build as --version.
func init() {
	if version != "" {
		refdoc.Version = version
	}
	if commit != "" {
		refdoc.Commit = commit
	}
}

// buildStamp is mtm's stamp: main.version and main.commit when linked in,
// else the build info (refdoc.BuildStamp).
func buildStamp() refdoc.Stamp { return refdoc.BuildStamp("mtm") }

// writeVersion prints the --version line: tool, version, commit and the
// ochazuke version it carries.
func writeVersion(w io.Writer, s refdoc.Stamp) error {
	v := s.Version
	if v == "" {
		v = "devel"
	}
	line := fmt.Sprintf("%s %s (commit %s", s.Tool, v, s.ShortCommit())
	if s.Date != "" {
		line += ", " + s.Date
	}
	if s.Ochazuke != "" {
		line += ", ochazuke " + s.Ochazuke
	}
	_, err := fmt.Fprintln(w, line+")")
	return err
}
