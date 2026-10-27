// Package hints assigns short labels to the items on a pane's screen and
// resolves typed keys to the item a label names.
//
// The label assignment (NormalizeAlphabet, Labels, KeyLabels, Target,
// Assign, Distance) and the typing rule (Matcher) are ported from tuios,
// internal/hints/labels.go and internal/app/hints.go (HintsPress) and
// internal/app/hints_scan.go (hintDistance):
//
//	MIT License, Copyright (c) 2025 Gaurav Gosain
//	https://github.com/Gaurav-Gosain/tuios
//
//	Permission is hereby granted, free of charge, to any person obtaining a
//	copy of this software and associated documentation files (the
//	"Software"), to deal in the Software without restriction, including
//	without limitation the rights to use, copy, modify, merge, publish,
//	distribute, sublicense, and/or sell copies of the Software, and to
//	permit persons to whom the Software is furnished to do so, subject to
//	the following conditions: The above copyright notice and this
//	permission notice shall be included in all copies or substantial
//	portions of the Software. THE SOFTWARE IS PROVIDED "AS IS", WITHOUT
//	WARRANTY OF ANY KIND.
//
// mtm's change: the default alphabet leaves out the letters whose Ctrl form
// a legacy terminal cannot tell from another key (UnsafeCtrlLetters), since
// Ctrl and a label opens the item.
package hints

import (
	"slices"
	"strings"
	"unicode"
)

// UnsafeCtrlLetters are the letters whose Ctrl form a legacy terminal sends
// as another key: ctrl+h is backspace, ctrl+i tab, ctrl+j and ctrl+m enter.
// DefaultAlphabet has none of them.
const UnsafeCtrlLetters = "hijm"

// DefaultAlphabet is the home row first (as tuios, tmux-fingers and kitty's
// hints kitten start), then the top and bottom rows, without
// UnsafeCtrlLetters and without c (ctrl+c is the app's quit key, so
// ctrl+label could not open an item labelled with it).
const DefaultAlphabet = "asdfgklqwertyuopzxvbn"

// NormalizeAlphabet makes a label alphabet usable: lowercase ASCII letters
// only, each once, in the order given. It returns DefaultAlphabet when
// fewer than two letters are left, because one letter cannot tell two
// matches apart. It does not remove UnsafeCtrlLetters: an alphabet the
// caller passes is the caller's choice.
//
// Letters only, and lowercase only, because the keys around a label have
// meanings of their own: Shift on a letter types the item, Ctrl on a letter
// opens it.
func NormalizeAlphabet(alphabet string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(alphabet) {
		if r > unicode.MaxASCII || r < 'a' || r > 'z' || strings.ContainsRune(b.String(), r) {
			continue
		}
		b.WriteRune(r)
	}
	if b.Len() < 2 {
		return DefaultAlphabet
	}
	return b.String()
}

// Labels returns n labels drawn from alphabet (normalized; "" is
// DefaultAlphabet), shortest first, none of them the start of another, so
// typing a label ends the moment it is complete.
//
// Every letter starts as a label of its own. While there are too few, the
// last of the shortest labels is taken apart into one longer label per
// letter. Each split costs one short label and adds len(alphabet) longer
// ones, so the labels stay as short as they can for the count, and the
// split eats the alphabet from its end, so the first letters stay one key
// long for as long as possible.
func Labels(n int, alphabet string) []string {
	return KeyLabels(n, NormalizeAlphabet(alphabet))
}

// KeyLabels is Labels over keys taken as they are, with no check of what
// they are. keys must hold at least two distinct runes.
func KeyLabels(n int, alphabet string) []string {
	if n <= 0 || len([]rune(alphabet)) < 2 {
		return nil
	}
	leaves := make([]string, 0, max(n, len(alphabet)))
	for _, r := range alphabet {
		leaves = append(leaves, string(r))
	}
	for len(leaves) < n {
		// leaves is ordered by length, so the shortest ones are at the
		// front and the last of them sits right before the first longer one.
		short := len(leaves[0])
		idx := 0
		for idx+1 < len(leaves) && len(leaves[idx+1]) == short {
			idx++
		}
		prefix := leaves[idx]
		leaves = slices.Delete(leaves, idx, idx+1)
		for _, r := range alphabet {
			leaves = append(leaves, prefix+string(r))
		}
	}
	rank := func(label string) []int {
		out := make([]int, 0, len(label))
		for _, c := range label {
			out = append(out, strings.IndexRune(alphabet, c))
		}
		return out
	}
	slices.SortStableFunc(leaves, func(a, b string) int {
		if len(a) != len(b) {
			return len(a) - len(b)
		}
		return slices.Compare(rank(a), rank(b))
	})
	return leaves[:n]
}

// Target is one occurrence on the screen, as label assignment sees it: its
// text, and how far it is from where the person is looking (Distance).
type Target struct {
	Text     string
	Distance int
	// Key, when set, is what makes two targets the same thing to type.
	// Targets with the same key share a label; an empty key is the text.
	// contextitems.Span.Key is the key for a span.
	Key string
}

// Assign gives every target a label and returns them in the targets' order.
//
// The same text (or Key) gets the same label wherever it appears. The
// texts nearest the person get the shortest labels: a text's distance is
// that of its nearest occurrence, and a tie goes to the text seen first.
func Assign(targets []Target, alphabet string) []string {
	type text struct {
		value    string
		distance int
		first    int
	}
	byText := map[string]*text{}
	var order []*text
	key := func(t Target) string {
		if t.Key != "" {
			return t.Key
		}
		return t.Text
	}
	for i, t := range targets {
		k := key(t)
		if e, ok := byText[k]; ok {
			e.distance = min(e.distance, t.Distance)
			continue
		}
		e := &text{value: k, distance: t.Distance, first: i}
		byText[k] = e
		order = append(order, e)
	}
	slices.SortStableFunc(order, func(a, b *text) int {
		if a.distance != b.distance {
			return a.distance - b.distance
		}
		return a.first - b.first
	})
	labels := Labels(len(order), alphabet)
	labelOf := make(map[string]string, len(order))
	for i, e := range order {
		labelOf[e.value] = labels[i]
	}
	out := make([]string, len(targets))
	for i, t := range targets {
		out[i] = labelOf[key(t)]
	}
	return out
}

// Distance is how far the cell (row, col) is from the cursor on a screen
// width columns wide, as tuios measures it: rows count first (a row away is
// further than any column), so the cursor's row is nearest, then the rows
// next to it. With the cursor on the bottom row this is bottom first. Pass
// a span's first cell (Span.Parts[0]).
func Distance(row, col, cursorRow, cursorCol, width int) int {
	dy := row - cursorRow
	if dy < 0 {
		dy = -dy
	}
	dx := col - cursorCol
	if dx < 0 {
		dx = -dx
	}
	return dy*(width+1) + dx
}
