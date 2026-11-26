package app

import (
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/runs"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/ui"
)

const runTestSummaryID = "runs.detail.testSummary"

func (a *App) compactTestOutput(rec data.Record, field, output string) (ui.ViewNode, bool) {
	if rec.Kind != data.KindRun || field != "output" ||
		(a.cfg != nil && a.cfg.Settings != nil && !a.cfg.Settings.Bool(SetCompactTestOutput, true)) {
		return ui.ViewNode{}, false
	}
	outcomes, err := data.ParseGoTestJSON(strings.NewReader(output))
	if err != nil || len(outcomes) == 0 {
		return ui.ViewNode{}, false
	}
	report := data.FormatCompactTestSummary(outcomes, false)
	status := rec.Text("runStatus")
	if status == runs.StatusFailed && report.Failed == 0 {
		return ui.ViewNode{}, false
	}
	partial := rec.Bool("outputTruncated") || status == runs.StatusRunning || status == runs.StatusCancelled
	return views.TestSummaryView(runTestSummaryID, views.Label(field), report, partial), true
}
