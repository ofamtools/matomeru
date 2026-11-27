package app

import (
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/table"
)

// Space and commands of the persistent Captures Review Page.
const (
	SpaceCaptures = "captures"

	CmdCapturesOpen            = "captures.open"
	cmdCaptureRowsMove         = "captures.rows.move"
	CmdCaptureEdit             = "captures.edit"
	CmdCaptureSendSelected     = "captures.send"
	CmdCapturePickSendSelected = "captures.send.pick"
	CmdCaptureArchiveSelected  = "captures.archive"
	CmdCaptureYankSelected     = "captures.yank"
	capturesFilterField        = "search"
)

// capturesController owns the Captures Review Page's filterable table state.
type capturesController struct {
	list          *table.Model
	visible       []data.CaptureRow
	total         int
	capturedCount int
	sentCount     int
	failedCount   int
}

func (a *App) initCaptures() {
	list := table.New(nil, nil)
	list.MatchMode = a.matchMode()
	list.FilterField = capturesFilterField
	a.captures = &capturesController{list: list}
	a.registerFilterable(views.CapturesListID, list)
}

func captureCommandSpecs() []core.CommandSpec {
	out := captureModalCommandSpecs()
	return append(out,
		spec(CmdCapturesOpen, "Open the Captures Review log page"),
		core.CommandSpec{
			ID:          cmdCaptureRowsMove,
			Description: "Move selection in the Captures Review log",
			Params:      []core.ParamSpec{{Name: "direction", Type: core.ParamString, Enum: []string{"up", "down"}}},
			Meta:        map[string]any{"internal": true},
		},
		spec(CmdCaptureEdit, "View or edit the selected capture in the Capture Compose modal"),
		spec(CmdCaptureSendSelected, "Send or re-send the selected capture to the active pinned agent (or pick an agent)"),
		spec(CmdCapturePickSendSelected, "Pick an agent pane and send the selected capture"),
		spec(CmdCaptureArchiveSelected, "Archive or restore the selected capture"),
		spec(CmdCaptureYankSelected, "Yank the selected capture's full body to the clipboard"),
	)
}

func captureBindings() []core.Binding {
	n, scope := ModeNormal, views.ScopeCaptures
	sel := "facts.hasSelection"
	out := captureModalBindings()
	return append(out,
		// Global binding to open Captures Review page (also leader-mirrored via ctrl+a g L).
		bind(n, "", "g L", CmdCapturesOpen, "captures log"),

		// Scoped actions on the Captures Review page.
		with(bind(n, scope, "j", cmdCaptureRowsMove, "next capture"), map[string]any{"direction": "down"}),
		with(bind(n, scope, "k", cmdCaptureRowsMove, "previous capture"), map[string]any{"direction": "up"}),
		hidden(with(bind(n, scope, "down", cmdCaptureRowsMove, "next capture"), map[string]any{"direction": "down"})),
		hidden(with(bind(n, scope, "up", cmdCaptureRowsMove, "previous capture"), map[string]any{"direction": "up"})),
		when(bind(n, scope, "enter", CmdCaptureEdit, "view/edit capture"), sel),
		when(bind(n, scope, "o", CmdCaptureEdit, "view/edit capture"), sel),
		when(bind(n, scope, "s", CmdCaptureSendSelected, "send to pinned agent"), sel),
		when(bind(n, scope, "S", CmdCapturePickSendSelected, "pick agent & send"), sel),
		when(bind(n, scope, "d", CmdCaptureArchiveSelected, "archive/restore"), sel),
		when(bind(n, scope, "y", CmdCaptureYankSelected, "yank body"), sel),
		bind(n, scope, "n", CmdCaptureCompose, "new capture"),
		hidden(bind(n, scope, "c", CmdCaptureCompose, "new capture")),
	)
}

func (a *App) registerCaptures() {
	a.registerCaptureModal()
	a.handle(CmdCapturesOpen, func(s core.State, _ core.Command) core.StepResult {
		return core.Next(a.openSpace(s, SpaceCaptures))
	})
	a.handle(cmdCaptureRowsMove, func(s core.State, c core.Command) core.StepResult {
		a.syncCaptures()
		c.ID = "motion." + c.Arg("direction")
		a.captures.list.HandleCommand(c)
		return core.Next(s)
	})
	a.handle(CmdCaptureEdit, a.captureEditSelected)
	a.handle(CmdCaptureSendSelected, a.captureSendSelected)
	a.handle(CmdCapturePickSendSelected, a.capturePickSendSelected)
	a.handle(CmdCaptureArchiveSelected, a.captureArchiveSelected)
	a.handle(CmdCaptureYankSelected, a.captureYankSelected)
}

// --- Sync & projection ---------------------------------------------------------

func (a *App) syncCaptures() {
	if a.captures == nil {
		return
	}
	cc := a.captures
	records := a.queryCaptureRecords()
	cc.total = len(records)
	cc.capturedCount, cc.sentCount, cc.failedCount = countCaptureStatuses(records)

	schema := a.schema(data.KindCapture)
	rows := make([]table.Row, 0, len(records))
	byID := make(map[string]data.CaptureRow, len(records))
	for _, rec := range records {
		cr := a.projectCaptureRow(schema, rec)
		byID[rec.ID] = cr
		rows = append(rows, table.Row{
			ID:    rec.ID,
			Cells: map[string]any{capturesFilterField: captureFilterText(cr)},
		})
	}
	shown := syncTableSelection(cc.list, rows, "")
	cc.visible = cc.visible[:0]
	for _, r := range shown {
		cc.visible = append(cc.visible, byID[r.ID])
	}
}

func (a *App) queryCaptureRecords() []data.Record {
	if a.Store == nil {
		return nil
	}
	res, err := a.Store.Query(data.Query{Kind: data.KindCapture, IncludeArchived: a.nav.showArchived})
	if err != nil {
		a.fail("load captures", err)
		return nil
	}
	records := a.scopeCaptureRecords(res.Items)
	data.SortCapturesNewestFirst(records)
	return records
}

func countCaptureStatuses(records []data.Record) (captured, sent, failed int) {
	for _, rec := range records {
		switch rec.Text("captureStatus") {
		case data.CaptureStatusSent:
			sent++
		case data.CaptureStatusFailed:
			failed++
		case data.CaptureStatusCaptured:
			captured++
		}
	}
	return captured, sent, failed
}

func (a *App) projectCaptureRow(schema data.Schema, rec data.Record) data.CaptureRow {
	status := rec.Text("captureStatus")
	if status == "" {
		status = data.CaptureStatusCaptured
	}
	icon, label, tone, _ := views.StatusIndicator(schema, "captureStatus", status)
	body := rec.Text("body")
	title := strings.TrimSpace(rec.Text("title"))
	if title == "" {
		title = data.CaptureTitleFromBody(body)
	}
	paneID, paneTitle := a.resolveRefTitle(rec, "pane")
	projectID, projectTitle := a.resolveRefTitle(rec, "project")
	ts := rec.UpdatedAt
	if ts.IsZero() {
		ts = rec.CreatedAt
	}
	return data.CaptureRow{
		ID:           rec.ID,
		Title:        title,
		Body:         body,
		Preview:      data.CapturePreview(body, 64),
		Status:       status,
		StatusLabel:  label,
		StatusIcon:   icon,
		StatusTone:   tone,
		Source:       rec.Text("source"),
		PaneID:       paneID,
		PaneTitle:    paneTitle,
		ProjectID:    projectID,
		ProjectTitle: projectTitle,
		Timestamp:    ts,
		SentAt:       rec.Time("sentAt"),
		Error:        rec.Text("error"),
		Archived:     rec.IsArchived(),
		Ref:          rec.Ref(),
	}
}

func (a *App) resolveRefTitle(rec data.Record, field string) (string, string) {
	ref, ok := rec.RefField(field)
	if !ok || ref.ID == "" {
		return "", ""
	}
	return ref.ID, a.title(ref)
}

func captureFilterText(r data.CaptureRow) string {
	return strings.Join([]string{r.Title, r.Body, r.Status, r.StatusLabel, r.PaneTitle, r.ProjectTitle, r.Source}, " ")
}

func (a *App) selectedCaptureRow() (data.CaptureRow, bool) {
	a.syncCaptures()
	row, ok := a.captures.list.FocusedRow()
	if !ok || row.ID == "" {
		a.say("no capture selected")
		return data.CaptureRow{}, false
	}
	idx := slices.IndexFunc(a.captures.visible, func(r data.CaptureRow) bool { return r.ID == row.ID })
	if idx < 0 {
		a.say("no capture selected")
		return data.CaptureRow{}, false
	}
	return a.captures.visible[idx], true
}

// --- View ----------------------------------------------------------------------

func (a *App) capturesSpace() ui.ViewNode {
	a.syncCaptures()
	cc := a.captures
	pinnedTitle := ""
	pinned, _, pinnedCount := a.activePinnedPane()
	if pinned != nil {
		pinnedTitle = views.Title(a.schema(data.KindPane), *pinned)
	}
	selIdx := cc.list.SelectedIdx
	if len(cc.visible) == 0 {
		selIdx = -1
	}
	return views.CapturesPageView(views.CapturesPageSpec{
		Rows:             cc.visible,
		SelectedIdx:      selIdx,
		Total:            cc.total,
		CapturedCount:    cc.capturedCount,
		SentCount:        cc.sentCount,
		FailedCount:      cc.failedCount,
		FilterQuery:      cc.list.FilterQuery(),
		PinnedAgentTitle: pinnedTitle,
		PinnedCount:      pinnedCount,
		ScopeLabel:       a.scopeModeBannerLabel(),
		Now:              a.now(),
	})
}

// --- Selected-row actions ------------------------------------------------------

func (a *App) captureEditSelected(s core.State, _ core.Command) core.StepResult {
	row, ok := a.selectedCaptureRow()
	if !ok {
		return core.Next(s)
	}
	return a.openCaptureCompose(s, core.Command{Args: map[string]any{"captureId": row.ID}})
}

func (a *App) captureSendSelected(s core.State, _ core.Command) core.StepResult {
	row, ok := a.selectedCaptureRow()
	if !ok {
		return core.Next(s)
	}
	pinned, _, _ := a.activePinnedPane()
	if pinned == nil {
		return core.Next(a.openCaptureAgentPicker(s, row.ID, row.Body, row.Source, row.ProjectID))
	}
	a.sendAndLogCapture(row.ID, row.Body, row.Source, *pinned, row.ProjectID)
	return core.Next(s)
}

func (a *App) capturePickSendSelected(s core.State, _ core.Command) core.StepResult {
	row, ok := a.selectedCaptureRow()
	if !ok {
		return core.Next(s)
	}
	return core.Next(a.openCaptureAgentPicker(s, row.ID, row.Body, row.Source, row.ProjectID))
}

func (a *App) captureArchiveSelected(s core.State, _ core.Command) core.StepResult {
	row, ok := a.selectedCaptureRow()
	if !ok || a.Store == nil {
		return core.Next(s)
	}
	if row.Archived {
		if _, _, err := a.Store.Restore(row.Ref, User); err != nil {
			a.fail("restore capture", err)
			return core.Next(s)
		}
		a.say("restored capture: %s", row.Title)
		return core.Next(s)
	}
	if _, _, err := a.Store.Archive(row.Ref, User); err != nil {
		a.fail("archive capture", err)
		return core.Next(s)
	}
	a.say("archived capture: %s", row.Title)
	return core.Next(s)
}

func (a *App) captureYankSelected(s core.State, _ core.Command) core.StepResult {
	row, ok := a.selectedCaptureRow()
	if !ok {
		return core.Next(s)
	}
	a.yank(row.Body, "capture body")
	return core.Next(s)
}
