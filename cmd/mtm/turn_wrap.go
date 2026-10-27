package main

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	xansi "github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/history"
	"github.com/unixsuperhero/matomeru/internal/turns"
)

// An opened turn's lines wrap to the width the tree draws them at, the way
// a chat shows a message: prose breaks at spaces, a token longer than the
// row (a URL, a path) breaks where the row ends, and a continuation keeps
// its line's indentation, so code stays under code. Each wrapped piece is
// a row of its own (a turn-line node), so j/k, the paging motions, G and
// gg move over what is on screen. The width is known only when the page
// draws, so the tree keeps what it was filled with (turnFill) and the
// draw re-fills it when the width changed (a resize, > and <, the detail
// moving below or beside the list): the first piece of a source line
// keeps the id it always had (<turn>/<line>), the others are
// <turn>/<line>.<piece>, so what is expanded and the marks on whole
// lines survive, and the selection stays on the piece holding the same
// part of the same source line. The / filter matches a source line whole
// (each piece carries its line as its one cell, which the filter reads
// and a list without columns does not draw), so a phrase split across
// pieces still finds every piece of its line.

// turnLineIndent is how far a turn-line row is drawn from the tree's left
// edge: one level of depth and the expand marker's column (Tree.prefix).
const turnLineIndent = 4

// turnFill is what a turns tree was last filled with, so a new width
// re-wraps it without reading the journal again.
type turnFill struct {
	prefix, window string
	back           turnRef
	turns          []history.PaneTurn
	width          int // the text width the lines were wrapped at; 0 (not drawn yet) does not wrap
	// A fill of turns read from the agent's log or the shell's marks
	// (turns_model.go) instead of the journal: model is set, mturns are the
	// turns, source what was read of the pane. known are the nodes whose
	// open or closed state was settled (a new one closes), built the open
	// turns whose lines the fill holds, nodes what the fill pushed.
	model  bool
	mturns []turns.Turn
	source *paneTurns
	known  map[string]bool
	built  map[string]bool
	nodes  []ochazuke.TreeNode
}

// turnPiece is one row of a wrapped line: its text and the rune offset in
// the source line where it starts.
type turnPiece struct {
	text   string
	offset int
}

// wrapTurnLine wraps a line to width cells. A line that fits, and any
// line at a width of 0 or less, is one piece. Spaces at a break are
// dropped; a piece's offset is where its first character is.
func wrapTurnLine(line string, width int) []turnPiece {
	if width <= 0 || xansi.StringWidth(line) <= width {
		return []turnPiece{{text: line}}
	}
	body := strings.TrimLeft(line, " ")
	indent := line[:len(line)-len(body)]
	// A continuation keeps the indentation while half the row is left.
	cont := indent[:min(len(indent), width/2)]
	var pieces []turnPiece
	var cur strings.Builder
	curWidth, start, words := 0, 0, false
	row := func(prefix string, at int) {
		cur.Reset()
		cur.WriteString(prefix)
		curWidth, start, words = len(prefix), at, false
	}
	emit := func() {
		pieces = append(pieces, turnPiece{text: cur.String(), offset: utf8.RuneCountInString(line[:start])})
	}
	row(indent, 0)
	for at := len(indent); at < len(line); {
		sp := at
		for sp < len(line) && line[sp] == ' ' {
			sp++
		}
		end := sp
		for end < len(line) && line[end] != ' ' {
			end++
		}
		if sp == end {
			break // trailing spaces
		}
		space, word := line[at:sp], line[sp:end]
		ww := xansi.StringWidth(word)
		switch {
		case !words:
		case curWidth+len(space)+ww <= width:
			cur.WriteString(space)
			curWidth += len(space)
		case ww <= width-len(cont) || curWidth+len(space) >= width:
			emit() // the word starts the next row
			row(cont, sp)
		default:
			cur.WriteString(space) // longer than a row: it starts here and breaks
			curWidth += len(space)
		}
		if !words && len(pieces) > 0 {
			start = sp
		}
		// Hard-break what does not fit, at a grapheme boundary.
		wordAt := sp
		for curWidth+ww > width {
			fit := xansi.Truncate(word, width-curWidth, "")
			if fit == "" || !strings.HasPrefix(word, fit) {
				if words {
					emit()
					row(cont, wordAt)
					continue
				}
				_, n := utf8.DecodeRuneInString(word)
				fit = word[:n] // a wide character on a row too narrow for it
			}
			cur.WriteString(fit)
			word, wordAt = word[len(fit):], wordAt+len(fit)
			ww = xansi.StringWidth(word)
			emit()
			row(cont, wordAt)
		}
		cur.WriteString(word)
		curWidth += ww
		words = true
		at = end
	}
	if words || len(pieces) == 0 {
		emit()
	}
	return pieces
}

// turnFillOf is a tree's fill state, made on first use.
func (a *App) turnFillOf(tree *ochazuke.Tree) *turnFill {
	if a.turnFills == nil {
		a.turnFills = map[string]*turnFill{}
	}
	st := a.turnFills[tree.ID()]
	if st == nil {
		st = &turnFill{}
		a.turnFills[tree.ID()] = st
	}
	return st
}

// turnNodes are the tree's nodes for a fill: a row per turn and, under
// it, a row per piece of each of its lines; times are shown in loc.
func turnNodes(st *turnFill, loc *time.Location) []ochazuke.TreeNode {
	var nodes []ochazuke.TreeNode
	for _, t := range st.turns {
		id := st.prefix + t.ID
		ref := st.back
		ref.Window, ref.ID, ref.Line = st.window, t.ID, -1
		nodes = append(nodes, ochazuke.TreeNode{ID: id, Label: turnLabel(t, loc), Noun: "turn", Data: ref})
		for j, line := range turnLines(t) {
			pieces := wrapTurnLine(line, st.width)
			for k, p := range pieces {
				ref.Line, ref.Offset = j, p.offset
				n := ochazuke.TreeNode{ID: fmt.Sprintf("%s/%d", id, j), Parent: id, Label: p.text, Noun: "turn-line", Data: ref}
				if k > 0 {
					n.ID = fmt.Sprintf("%s/%d.%d", id, j, k)
				}
				if len(pieces) > 1 {
					n.Cells = []string{line} // the filter's haystack: the line whole
				}
				nodes = append(nodes, n)
			}
		}
	}
	return nodes
}

// setTurnNodes pushes a fill to its tree; a selected line piece moves to
// the piece now holding the same place in the same source line.
func (a *App) setTurnNodes(tree *ochazuke.Tree, st *turnFill) error {
	var was turnRef
	if n, ok := tree.Selected(); ok && n.Noun == "turn-line" {
		was, _ = n.Data.(turnRef)
	}
	var nodes []ochazuke.TreeNode
	if st.model {
		nodes = a.modelTurnNodes(tree, st)
	} else {
		nodes = turnNodes(st, a.location())
	}
	st.nodes = nodes
	if err := tree.SetNodes(nodes); err != nil {
		return err
	}
	if was.ID == "" {
		return nil
	}
	target := ""
	for _, n := range nodes {
		ref, _ := n.Data.(turnRef)
		if n.Noun == "turn-line" && ref.ID == was.ID && ref.Line == was.Line && ref.Offset <= was.Offset {
			target = n.ID
		}
	}
	if now, ok := tree.Selected(); target == "" || ok && now.ID == target {
		return nil
	}
	return tree.Select(target)
}

// wrapTurns re-fills a turns tree when the width it is drawn at (the
// width passed to its Block) is not the one its lines were wrapped at.
func (a *App) wrapTurns(tree *ochazuke.Tree, width int) {
	st := a.turnFillOf(tree)
	w := width - turnLineIndent
	if len(tree.List().Marked()) > 0 {
		w -= 2 // the marks' gutter
	}
	if w < 1 || w == st.width && !(st.model && turnsNeedBuild(tree, st)) {
		return
	}
	st.width = w
	var err error
	if st.model {
		err = a.refillModelTurns(tree, st)
	} else {
		err = a.setTurnNodes(tree, st)
	}
	if err != nil {
		a.r.UI().Status(err.Error())
	}
}
