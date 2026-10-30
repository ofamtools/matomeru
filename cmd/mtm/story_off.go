//go:build !monogatari

package main

import "io"

// storyShell handles nothing in a normal build: only the tester (-tags
// monogatari, story.go) has mtm story, so the shipped binary links no
// monogatari and mtm story stays the o-story pass-through.
func storyShell([]string, io.Reader, io.Writer, io.Writer) (bool, error) { return false, nil }
