package views

import (
	"fmt"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
)

// Focus & Pomodoro Control Center scope and node IDs.
const (
	// ScopeFocusCenter is the page's own scope, apart from any noun's.
	ScopeFocusCenter  = "focus.center"
	FocusCenterPageID = "focus.center"
	FocusProjectsID   = "focus.projects"
	FocusBannerID     = "focus.banner"
	FocusSummaryID    = "focus.summary"
	FocusHintID       = "focus.hint"
	// FocusCenterHint names the page's verbs under the table.
	FocusCenterHint = "space/p priority today · s start Pomodoro · x stop · b time budget · w focus window · W window on/off · c capture · enter open · / filter"

	// FocusPriorityMark and FocusLockMark are the table's two symbols.
	FocusPriorityMark = "★"
	FocusLockMark     = "🔒"
)

// FocusCenterSpec is the pure data contract of FocusCenterView.
type FocusCenterSpec struct {
	// Rows are the table's projects in display order (already filtered by
	// the caller); SelectedIdx is the focused one, or -1 for none. Total
	// counts every project, whatever the filter shows.
	Rows        []data.FocusProjectRow
	SelectedIdx int
	Total       int
	FilterQuery string
	// Pomodoro is the running Pomodoro, if any.
	Pomodoro *data.PomodoroSession
	// Window is the scheduled window open now; Pending is one that came due
	// and waits for its report.
	Window  *data.FocusWindowRule
	Pending *data.FocusWindowRule
	// Budgets and Windows are everything configured, for the summary strip.
	Budgets []data.ProjectTimeBudget
	Windows []data.FocusWindowRule
	// ProjectTitles names projects by ID.
	ProjectTitles map[string]string
	// CaptureBucketID is the capture bucket's project, "" when none is set.
	CaptureBucketID string
	// PriorityCount is how many projects are a priority today.
	PriorityCount int
	// HideNonPriority and EnforceLocks say what the focus settings do.
	HideNonPriority bool
	EnforceLocks    bool
	// Now is the clock countdowns are told against.
	Now time.Time
}

func (spec FocusCenterSpec) projectTitle(id string) string {
	if t := strings.TrimSpace(spec.ProjectTitles[id]); t != "" {
		return t
	}
	if id == "" {
		return "no project"
	}
	return id
}

func (spec FocusCenterSpec) projectTitles(ids []string) string {
	if len(ids) == 0 {
		return "no project"
	}
	names := make([]string, len(ids))
	for i, id := range ids {
		names[i] = spec.projectTitle(id)
	}
	return strings.Join(names, ", ")
}

// FocusCenterView presents the Focus & Pomodoro Control Center. It reads
// nothing but its spec.
func FocusCenterView(spec FocusCenterSpec) ui.ViewNode {
	filtering := strings.TrimSpace(spec.FilterQuery) != ""
	facts := map[string]any{
		"count":          len(spec.Rows),
		"total":          spec.Total,
		"hasFilter":      filtering,
		"hasSelection":   false,
		"projectId":      "",
		"pomodoroActive": spec.Pomodoro.Running(spec.Now),
		"windowActive":   spec.Window != nil,
		"isPriority":     false,
		"isLocked":       false,
		"hasBudget":      false,
		"hasWindow":      false,
		"priorityCount":  spec.PriorityCount,
	}
	selected := -1
	if i := spec.SelectedIdx; i >= 0 && i < len(spec.Rows) {
		selected = i
		row := spec.Rows[i]
		facts["hasSelection"] = true
		facts["projectId"] = row.ProjectID
		facts["isPriority"] = row.Priority
		facts["isLocked"] = row.Access.Locked
		facts["hasBudget"] = row.Budget != nil
		facts["hasWindow"] = len(row.Windows) > 0
	}

	children := []ui.ViewNode{focusBanner(spec)}
	if filtering {
		children = append(children, fixed(text("focus.filter", " /"+spec.FilterQuery, "accent"), 1))
	}
	children = append(children,
		focusProjectsTable(spec, selected, filtering),
		focusSummary(spec),
		fixed(text(FocusHintID, " "+FocusCenterHint, "muted"), 1),
	)
	return ui.ViewNode{
		ID:       FocusCenterPageID,
		Kind:     ui.KindVStack,
		Scope:    ScopeFocusCenter,
		Box:      layout.Constraints{Flex: 1},
		Facts:    facts,
		Children: children,
	}
}

// FocusCountdown tells a remaining time in whole minutes, rounded up:
// "25m", "1h 05m", and "<1m" at the end.
func FocusCountdown(d time.Duration) string {
	if d <= 0 {
		return "0m"
	}
	if d < time.Minute {
		return "<1m"
	}
	mins := int((d + time.Minute - 1) / time.Minute)
	if mins >= 60 {
		return fmt.Sprintf("%dh %02dm", mins/60, mins%60)
	}
	return fmt.Sprintf("%dm", mins)
}

// FocusClock tells a time of day as HH:MM.
func FocusClock(t time.Time) string { return t.Format("15:04") }

// focusBanner is the page's first line: the running Pomodoro, else the open
// scheduled window, else a window waiting for its report, else nothing on.
func focusBanner(spec FocusCenterSpec) ui.ViewNode {
	var line spanLine
	line.write(" Focus ", "accent")
	lockNote := " · other projects locked"
	if !spec.EnforceLocks {
		lockNote = " · locks off (focus.enforceLocks)"
	}
	switch {
	case spec.Pomodoro.Running(spec.Now):
		p := spec.Pomodoro
		name := p.Title
		if name == "" {
			name = "Pomodoro"
		}
		line.write(" ▶ "+name, "state.working")
		line.write(" "+FocusCountdown(p.Remaining(spec.Now))+" left", "accent")
		line.write(" · ends "+FocusClock(p.EndsAt)+" · on "+spec.projectTitles(p.AllowedProjectIDs), "muted")
		line.write(lockNote, "warning")
	case spec.Window != nil:
		w := spec.Window
		line.write(" ▶ "+w.Name+" window open", "state.working")
		if _, end, ok := w.Span(spec.Now); ok {
			line.write(" until "+FocusClock(end), "accent")
		}
		line.write(" · on "+spec.projectTitle(w.TargetProjectID), "muted")
		if w.LockOtherProjects {
			line.write(lockNote, "warning")
		}
	case spec.Pending != nil:
		line.write(" ◔ "+spec.Pending.Name+" window is due", "warning")
		line.write(" · waiting for its report: "+spec.Pending.ReportCommand, "muted")
	default:
		line.write(" ○ no Pomodoro running", "muted")
		line.write(" · s starts one on the selected project", "muted")
	}
	if spec.PriorityCount > 0 {
		line.write(fmt.Sprintf("   %s %d priority today", FocusPriorityMark, spec.PriorityCount), "accent")
		if spec.HideNonPriority {
			line.write(" · other projects hidden in the outline", "muted")
		}
	}
	return fixed(ui.ViewNode{
		ID:    FocusBannerID,
		Kind:  ui.KindText,
		Props: map[string]any{"text": line.String(), "spans": line.spans},
	}, 1)
}

// focusColumn is one column of the projects table: its heading and how a row
// fills its cell (text and tone).
type focusColumn struct {
	Title string
	Cell  func(row data.FocusProjectRow, now time.Time) (string, string)
}

// focusColumns are the projects table's columns in order.
var focusColumns = []focusColumn{
	{"Today", func(r data.FocusProjectRow, _ time.Time) (string, string) {
		if r.Priority {
			return FocusPriorityMark + " priority", "accent"
		}
		return "", ""
	}},
	{"Lock", FocusLockCell},
	{"Minutes / Budget", FocusBudgetCell},
	{"Project", func(r data.FocusProjectRow, _ time.Time) (string, string) {
		title := r.Title
		if r.CaptureBucket {
			title += "  (capture bucket)"
		}
		for _, w := range r.Windows {
			state := "off"
			if w.Enabled {
				state = w.DailyTimeHHMM
			}
			title += fmt.Sprintf("  [%s %s]", w.Name, state)
		}
		return title, ""
	}},
}

// FocusLockCell tells a project's lock: the mark, why, and when it ends.
func FocusLockCell(r data.FocusProjectRow, _ time.Time) (string, string) {
	if !r.Access.Locked {
		return "—", "muted"
	}
	cell := FocusLockMark + " " + r.Access.Reason
	if !r.Access.UnlockAt.IsZero() {
		cell += " · until " + FocusClock(r.Access.UnlockAt)
	}
	return cell, "warning"
}

// FocusBudgetCell tells today's minutes on a project against its guardrail:
// "42m", "42m / 90m per 180m, cooldown 1 Pomodoro", "42m / 240m a day".
func FocusBudgetCell(r data.FocusProjectRow, _ time.Time) (string, string) {
	cell := fmt.Sprintf("%dm", r.TodayMinutes)
	b := r.Budget
	if b == nil || b.Empty() {
		if r.TodayMinutes == 0 {
			return "—", "muted"
		}
		return cell, ""
	}
	tone := ""
	if b.DailyMaxMinutes > 0 && r.TodayMinutes >= b.DailyMaxMinutes {
		tone = "warning"
	}
	return cell + " / " + focusBudgetLimits(b), tone
}

func focusProjectsTable(spec FocusCenterSpec, selected int, filtering bool) ui.ViewNode {
	titles := make([]any, len(focusColumns))
	for i, col := range focusColumns {
		titles[i] = col.Title
	}
	rows := make([]any, 0, len(spec.Rows))
	for _, r := range spec.Rows {
		cells := make([]any, len(focusColumns))
		tones := make([]string, len(focusColumns))
		for i, col := range focusColumns {
			cells[i], tones[i] = col.Cell(r, spec.Now)
		}
		rows = append(rows, ui.TableRowSpec{Cells: cells, CellTones: tones})
	}
	table := ui.ViewNode{
		ID:        FocusProjectsID,
		Kind:      ui.KindTable,
		Focusable: true,
		Filter:    &ui.FilterSpec{ID: FocusProjectsID, Prefix: "projects ", Fields: []string{"project", "lock", "priority"}},
		Box:       layout.Constraints{Flex: 1},
		Props: map[string]any{
			"columns":  titles,
			"rows":     rows,
			"selected": selected,
		},
	}
	if len(rows) == 0 {
		empty := "No projects yet · c captures an item into the capture bucket"
		if filtering {
			empty = "No projects match the filter."
		}
		table.Kind = ui.KindText
		table.Tone = "muted"
		table.Props = map[string]any{"text": " " + empty}
	}
	return table
}

// focusSummary is the strip under the table: the configured time-budget
// guardrails, the scheduled windows and the capture bucket.
func focusSummary(spec FocusCenterSpec) ui.ViewNode {
	var line spanLine
	line.write(" Guardrails ", "accent")
	if len(spec.Budgets) == 0 {
		line.write("none · b sets one", "muted")
	}
	for i, b := range spec.Budgets {
		if i > 0 {
			line.write(" · ", "muted")
		}
		line.write(spec.projectTitle(b.ProjectID)+" "+focusBudgetLimits(&b), "")
	}
	line.write("   Windows ", "accent")
	if len(spec.Windows) == 0 {
		line.write("none · w adds one", "muted")
	}
	for i, w := range spec.Windows {
		if i > 0 {
			line.write(" · ", "muted")
		}
		label := fmt.Sprintf("%s %s %dm on %s", w.Name, w.DailyTimeHHMM, w.DurationMin, spec.projectTitle(w.TargetProjectID))
		if w.LockOtherProjects {
			label += " " + FocusLockMark
		}
		tone := ""
		if !w.Enabled {
			label, tone = label+" (off)", "muted"
		}
		line.write(label, tone)
	}
	line.write("   Capture ", "accent")
	if spec.CaptureBucketID == "" {
		line.write("c creates the "+data.CaptureBucketTitle, "muted")
	} else {
		line.write(spec.projectTitle(spec.CaptureBucketID), "")
	}
	return fixed(ui.ViewNode{
		ID:    FocusSummaryID,
		Kind:  ui.KindText,
		Props: map[string]any{"text": line.String(), "spans": line.spans},
	}, 1)
}

// focusBudgetLimits tells a guardrail's limits and its cooldown.
func focusBudgetLimits(b *data.ProjectTimeBudget) string {
	var parts []string
	if b.WindowMinutes > 0 {
		parts = append(parts, fmt.Sprintf("%dm per %dm", b.WindowMinutes, int(b.RollingSpan()/time.Minute)))
	}
	if b.DailyMaxMinutes > 0 {
		parts = append(parts, fmt.Sprintf("%dm a day", b.DailyMaxMinutes))
	}
	if b.WindowMinutes > 0 {
		unit := "Pomodoro"
		if b.CooldownPomodoros > 1 {
			unit = "Pomodoros"
		}
		parts = append(parts, fmt.Sprintf("cooldown %d %s", max(b.CooldownPomodoros, 1), unit))
	}
	return strings.Join(parts, ", ")
}
