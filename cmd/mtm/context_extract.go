package main

import (
	"context"
	"errors"
	"fmt"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/contextitems"
	"ofam.dev/matomeru/internal/daemon"
	"ofam.dev/matomeru/internal/work"
)

// contextDaemon is the part of the daemon extraction reads: a pane's
// scrollback as plain text and its shell's command history. The panes
// daemon (tuios) provides it; a fake that does not has no extraction.
type contextDaemon interface {
	// Scrollback reads the recent buffer from line from (0-based; 0 is
	// all of it).
	Scrollback(ctx context.Context, p pane, from int) (scrollback, error)
	Commands(ctx context.Context, p pane) ([]daemon.CommandEntry, error)
}

// scrollback is a capture of a pane's recent buffer (history then the
// visible screen) and how many of its lines are history.
type scrollback struct {
	Text    string
	History int
}

// Scrollback is the pane's recent buffer from line from, unstyled.
func (t tuios) Scrollback(ctx context.Context, p pane, from int) (scrollback, error) {
	ctx, cancel := context.WithTimeout(ctx, daemonTimeout)
	defer cancel()
	params := daemon.CaptureParams{Session: p.Session, Window: p.ID, Source: "recent"}
	if from > 0 {
		start := from + 1 // capture-pane counts lines from 1
		params.Start = &start
	}
	got, err := t.Capture(ctx, params)
	return scrollback{Text: got.Content, History: got.HistoryRows}, err
}

// Commands is the pane's shell command history.
func (t tuios) Commands(ctx context.Context, p pane) ([]daemon.CommandEntry, error) {
	ctx, cancel := context.WithTimeout(ctx, daemonTimeout)
	defer cancel()
	return t.History(ctx, daemon.HistoryParams{Session: p.Session, Window: p.ID, Limit: 200})
}

// contextd is the daemon extraction reads, when the panes daemon has one.
func (a *App) contextd() (contextDaemon, bool) {
	d, ok := a.panesd.(contextDaemon)
	return d, ok
}

// extracted is what one extraction wrote.
type extracted struct {
	Pane  string              `json:"pane"`
	Found int                 `json:"found"`
	New   int                 `json:"new"`
	Items []contextitems.Item `json:"items"`
}

// extractContext is context-item.extract: the pane's whole scrollback and
// its command history, on demand, off the loop. The pane is --target (a
// pane row) or --pane (a window id or name).
func (a *App) extractContext(ctx ochazuke.Ctx) ochazuke.Result {
	d, ok := a.contextd()
	if !ok {
		return ochazuke.Failed(errors.New("context extraction needs the TUIOS daemon"))
	}
	var want pane
	if ctx.Instance != nil && ctx.Instance.Noun == "pane" {
		p, err := a.paneAt(ctx.Instance)
		if err != nil {
			return ochazuke.Failed(err)
		}
		want = p
	}
	name := ochazuke.StringArg(ctx.Args, "pane", "")
	if want.ID == "" && name == "" {
		return ochazuke.Failed(errors.New("name the pane: --target pane:SESSION/WINDOW or --pane WINDOW"))
	}
	origins := a.contextExtraction()
	panesd, ws, store := a.panesd, a.work, a.contexts
	return ochazuke.Pending(func(c context.Context) (any, error) {
		p := want
		if p.ID == "" {
			panes, err := panesd.ListPanes(c)
			if err != nil {
				return nil, err
			}
			for _, q := range panes {
				if q.ID != "" && (q.ID == name || q.Name == name || q.label() == name) {
					p = q
					break
				}
			}
			if p.ID == "" {
				return nil, fmt.Errorf("no pane %q", name)
			}
		}
		owner, err := ws.PaneOwner(p.ID, p.SessionID)
		if err != nil {
			return nil, err
		}
		var items []contextitems.Item
		if extractsFrom(origins, contextSource(p, "scrollback")) {
			back, err := d.Scrollback(c, p, 0)
			if err != nil {
				return nil, fmt.Errorf("capture %s: %w", p.label(), err)
			}
			items = contextitems.Extract(back.Text, p.Width, contextSource(p, "scrollback"))
		}
		// History is best effort: a pane without shell integration has none.
		if extractsFrom(origins, contextSource(p, "history")) {
			if commands, err := d.Commands(c, p); err == nil {
				for _, cmd := range commands {
					src := contextSource(p, "history")
					src.EntryKind, src.At = "command", cmd.At
					if cmd.Cwd != "" {
						src.Cwd = cmd.Cwd
					}
					items = append(items, contextitems.Extract(cmd.Cmdline, 0, src)...)
				}
			}
		}
		written, added, err := store.Merge(items, contextitems.Owner{ProjectID: owner.ProjectID, TaskID: owner.TaskID}, work.Now())
		return extracted{Pane: p.label(), Found: len(written), New: added, Items: written}, err
	}).Then(func(ctx ochazuke.Ctx, v any, err error) {
		if err != nil {
			return
		}
		if err := a.reloadContext(); err != nil {
			ctx.Error(err.Error())
		}
	})
}
