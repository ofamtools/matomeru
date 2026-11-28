package mcpui

import "ofam.dev/ochazuke/ui"

// CreatePageArgs are the arguments of ui.create_page.
type CreatePageArgs struct {
	Pane     string      `json:"pane,omitempty"`
	Task     string      `json:"task,omitempty"`
	ID       string      `json:"id,omitempty"`
	Title    string      `json:"title"`
	Slug     string      `json:"slug,omitempty"`
	PageKind string      `json:"pageKind,omitempty"`
	Pinned   bool        `json:"pinned,omitempty"`
	BaseDir  string      `json:"baseDir,omitempty"`
	Cwd      string      `json:"cwd,omitempty"`
	Root     ui.ViewNode `json:"root"`
}

// UpdatePageArgs are the arguments of ui.update_page. Page is a page ID, or
// a slug within Pane. The content is given as node, card or root; they are
// the same thing under the name that reads best for the mode.
type UpdatePageArgs struct {
	Pane     string       `json:"pane,omitempty"`
	Page     string       `json:"page"`
	Mode     string       `json:"mode"`
	Node     *ui.ViewNode `json:"node,omitempty"`
	Card     *ui.ViewNode `json:"card,omitempty"`
	Root     *ui.ViewNode `json:"root,omitempty"`
	TargetID string       `json:"targetId,omitempty"`
	Title    string       `json:"title,omitempty"`
	BaseDir  *string      `json:"baseDir,omitempty"`
	Cwd      *string      `json:"cwd,omitempty"`
}
