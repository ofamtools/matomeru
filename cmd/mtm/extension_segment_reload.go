package main

import (
	tea "charm.land/bubbletea/v2"
	"ofam.dev/ochazuke"
)

// An extension's header segment (its catalog's segments, mounted as
// <name>-<id>) answers a tool's state every few seconds; mtm otherwise
// reloads an extension page only when it opens and after one of its
// verbs. When a segment's text changes and one of the extension's pages
// is open, that extension's pages reload, so a tool that declares a state
// segment keeps its page live with no timer of its own: one reload per
// change, none while the text holds. extensions.segment-reload turns it
// off.

const settingExtSegmentReload = "extensions.segment-reload"

var extSegmentReloadSetting = ochazuke.Setting{Name: settingExtSegmentReload, Owner: extensionsOwner, Section: ochazuke.SettingsGeneralSection,
	Type: ochazuke.SettingBoolean, Default: true,
	Description: "Reload an extension's open page when one of its header segments changes; off reloads it only on open and after its verbs"}

// extSegmentOf is the extension a segment id belongs to, or nil.
func (a *App) extSegmentOf(id string) *extension {
	for _, x := range a.exts {
		for _, s := range x.doc.Segments {
			if x.name+"-"+s.ID == id {
				return x
			}
		}
	}
	return nil
}

// segmentText is a segment's last answer and error.
func (a *App) segmentText(id string) string {
	for _, s := range a.r.Segments() {
		if s.ID == id {
			return s.Text + "\x00" + s.Error
		}
	}
	return ""
}

// extSegmentBefore notes, before a segment's answer lands, which
// extension it belongs to and its text so far (nil: not an extension's).
func (a *App) extSegmentBefore(msg tea.Msg) (x *extension, id, before string) {
	m, ok := msg.(ochazuke.SegmentMsg)
	if !ok || a.cli {
		return nil, "", ""
	}
	if x = a.extSegmentOf(m.ID); x == nil {
		return nil, "", ""
	}
	return x, m.ID, a.segmentText(m.ID)
}

// extSegmentAfter reloads x's pages when the answer changed the segment
// and one of them is open.
func (a *App) extSegmentAfter(x *extension, id, before string) tea.Cmd {
	if x == nil || !a.r.Settings().Bool(settingExtSegmentReload) || a.segmentText(id) == before {
		return nil
	}
	for _, v := range x.views {
		if v.Page() == a.r.Page {
			a.reloadExtension(x)
			return a.r.PendingCommands()
		}
	}
	return nil
}
