package main

import (
	"strings"

	"github.com/unixsuperhero/ochazuke"
)

// settingDraftPerTarget keeps a draft per pane; off, the box holds one
// text whichever pane it shows (typed once, sent to whichever pane the box shows).
const settingDraftPerTarget = "prompt.draft-per-target"

var draftPerTargetSetting = ochazuke.Setting{Name: settingDraftPerTarget, Type: ochazuke.SettingBoolean, Default: true,
	Description: "Give each pane its own prompt draft: the box shows the draft of the pane it writes to, and text typed for one pane stays that pane's; off, the box keeps one text across panes"}

// promptDraft is one pane's unsent prompt: what its box held when the box
// last showed another pane, with the input's undo tree and the images
// attached to it. Drafts are never thrown away: a pane that goes keeps
// its draft for as long as mtm runs.
type promptDraft struct {
	text        string
	cursor      int
	history     []ochazuke.EditNode
	head        int
	attachments []promptAttachment
}

// syncPromptDraft gives the box the draft of the pane it now writes to.
// The box is one input; when its pane changes, the text (with its undo
// history and attachments) is kept as the old pane's draft and the new
// pane's draft (empty for a pane never written to) takes its place, so
// text typed for one pane is never shown for, or sent to, another. With
// no box shown nothing moves: the input stays the last pane's.
func (a *App) syncPromptDraft() {
	if a.pb == nil || a.pb.input == nil {
		return
	}
	p := a.promptPane()
	if p == nil || p.ID == a.pb.owner {
		return
	}
	in := a.pb.input
	if a.pb.owner == "" || !a.r.Settings().Bool(settingDraftPerTarget) {
		// The first pane the box shows owns what it holds; with one text
		// for every pane (the setting off) the text follows the box.
		a.pb.owner = p.ID
		return
	}
	if a.pb.drafts == nil {
		a.pb.drafts = map[string]*promptDraft{}
	}
	a.pb.drafts[a.pb.owner] = &promptDraft{
		text: string(in.Text), cursor: in.Cursor,
		history: in.History, head: in.Head,
		attachments: a.pb.attachments,
	}
	a.pb.owner = p.ID
	d := a.pb.drafts[p.ID]
	delete(a.pb.drafts, p.ID)
	if d == nil {
		d = &promptDraft{history: []ochazuke.EditNode{{Parent: -1}}}
	}
	in.SetText(d.text)
	in.History, in.Head = d.history, d.head
	in.Cursor = min(max(0, d.cursor), len(in.Text))
	a.pb.attachments = d.attachments
}

// promptDraftText is the unsent text written for the pane: the box's when
// the box is the pane's, else the draft kept for it.
func (a *App) promptDraftText(id string) string {
	if a.pb == nil || a.pb.input == nil {
		return ""
	}
	if id == a.pb.owner {
		return string(a.pb.input.Text)
	}
	if d := a.pb.drafts[id]; d != nil {
		return d.text
	}
	return ""
}

// clearPromptDraft empties the pane's draft once text sent from it
// reached the pane, unless it was edited since (it no longer reads sent).
// It reports whether it cleared.
func (a *App) clearPromptDraft(id, sent string) (bool, error) {
	if id == a.pb.owner {
		in := a.pb.input
		if string(in.Text) != sent {
			return false, nil
		}
		a.pb.attachments = nil
		return true, in.Replace(0, len(in.Text), "")
	}
	d := a.pb.drafts[id]
	if d == nil || d.text != sent {
		return false, nil
	}
	// As Replace would: the empty text is a new node of the draft's undo
	// tree, so u in its box brings the sent text back.
	if len(d.history) == 0 {
		d.history, d.head = []ochazuke.EditNode{{Text: d.text, Cursor: d.cursor, Parent: -1}}, 0
	}
	d.history[d.head].Children = append(d.history[d.head].Children, len(d.history))
	d.history = append(d.history, ochazuke.EditNode{Parent: d.head})
	d.head = len(d.history) - 1
	d.text, d.cursor, d.attachments = "", 0, nil
	return true, nil
}

// promptDraftFor reports whether the box holds, or keeps, an unsent draft
// written for the pane.
func (a *App) promptDraftFor(p pane) bool {
	return strings.TrimSpace(a.promptDraftText(p.ID)) != ""
}
