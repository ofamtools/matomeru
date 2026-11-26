package views

import (
	"fmt"
	"strconv"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/ui"
)

// TestSummaryView presents the test report as toned symbols, totals, and logs
// for failed tests. Partial reports summarize the captured events so far.
func TestSummaryView(id, title string, report data.CompactTestReport, partial bool) ui.ViewNode {
	if partial {
		title += " · partial test results"
	}
	runs := make([]ui.InlineRun, 0, len(report.Runs))
	for _, run := range report.Runs {
		runs = append(runs, ui.InlineRun{Text: run.Text, Tone: run.Tone})
	}
	children := []ui.ViewNode{
		text(id+".title", title, "accent"),
		{ID: id + ".strip", Kind: ui.KindText, Props: map[string]any{"runs": runs, "wrap": true}},
		text(id+".counts", fmt.Sprintf("%d passed · %d failed · %d skipped", report.Passed, report.Failed, report.Skipped), "muted"),
	}
	for i, failure := range report.FailureDetails {
		prefix := id + ".failure." + strconv.Itoa(i)
		label := strings.TrimSpace(failure.Package + " " + failure.Name)
		children = append(children, text(prefix+".title", "✗ "+label, "error"))
		if output := strings.TrimRight(failure.Output, "\r\n"); output != "" {
			children = append(children, ui.ViewNode{ID: prefix + ".output", Kind: ui.KindText, Tone: "md.code",
				Props: map[string]any{"text": output, "wrap": true}})
		}
	}
	return ui.ViewNode{ID: id, Kind: ui.KindVStack, Children: children}
}
