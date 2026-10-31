package main

import (
	"reflect"

	"github.com/unixsuperhero/ochazuke"
)

// Each visible panel keeps one exact content/geometry/style snapshot. Its
// body is still queried so component placement, follow and stream resizing
// remain live. Equal text skips the border layout and repeated cell fitting.
type panePanelCache struct {
	valid         bool
	title, tone   string
	width, height int
	focused       bool
	styles        ochazuke.Styles
	body, block   ochazuke.Block
}

func copyPaneBlock(b ochazuke.Block) ochazuke.Block {
	if b.Cursor != nil {
		p := *b.Cursor
		b.Cursor = &p
	}
	return b
}

func samePaneBlock(a, b ochazuke.Block) bool {
	return a.Text == b.Text && (a.Cursor == nil && b.Cursor == nil || a.Cursor != nil && b.Cursor != nil && *a.Cursor == *b.Cursor)
}

func (a *App) cachedPanePanel(c *panePanelCache, l ochazuke.Layout, title, tone string, body ochazuke.Block, width, height int, focused bool) ochazuke.Block {
	if a.r.Settings().Int(ochazuke.RenderMaxFPSSetting) > 0 && c.valid && c.title == title && c.tone == tone && c.width == width && c.height == height && c.focused == focused && samePaneBlock(c.body, body) && reflect.DeepEqual(c.styles, l.Styles) {
		return copyPaneBlock(c.block)
	}
	out := l.WithPanelTone(tone).PanelBlock(title, body, width, height, focused)
	*c = panePanelCache{valid: true, title: title, tone: tone, width: width, height: height, focused: focused, styles: l.Styles, body: copyPaneBlock(body), block: copyPaneBlock(out)}
	return out
}

// SetNodes rebuilds tree indexes and filtered rows. A listing that supplies
// identical nodes must leave those caches intact.
// Compare domain data too: equal labels alone do not imply equal actions.
func (a *App) setSectionNodes(s *paneSection, nodes []ochazuke.TreeNode) error {
	if a.r.Settings().Int(ochazuke.RenderMaxFPSSetting) > 0 && reflect.DeepEqual(s.tree.Nodes(), nodes) {
		return nil
	}
	return s.tree.SetNodes(nodes)
}

type paneColumnsCache struct {
	head, left, right string
	width, height     int
	text              string
}
