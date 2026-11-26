package views

import (
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/ui"
)

var focusViewNow = time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

func focusViewSpec() FocusCenterSpec {
	cfg := data.TogglePriorityProject(data.FocusConfig{CaptureBucketID: "bucket"}, "apollo", focusViewNow)
	cfg = data.SetTimeBudget(cfg, data.ProjectTimeBudget{ProjectID: "borealis", WindowMinutes: 90, DailyMaxMinutes: 240, CooldownPomodoros: 2})
	cfg = data.RecordUsage(cfg, "borealis", focusViewNow.Add(-42*time.Minute), 42)
	cfg, _ = data.UpsertWindow(cfg, data.FocusWindowRule{Name: "PR Reviews", TargetProjectID: "apollo", DailyTimeHHMM: "10:00", DurationMin: 45, LockOtherProjects: true, Enabled: true})
	cfg = data.StartPomodoro(cfg, "Parser", []string{"apollo"}, 25, "", focusViewNow.Add(-7*time.Minute))
	titles := map[string]string{"apollo": "Apollo", "borealis": "Borealis", "bucket": data.CaptureBucketTitle}
	projects := []data.FocusProject{{ID: "apollo", Title: "Apollo"}, {ID: "borealis", Title: "Borealis"}, {ID: "bucket", Title: data.CaptureBucketTitle}}
	return FocusCenterSpec{
		Rows: data.BuildFocusProjectRows(cfg, projects, focusViewNow), SelectedIdx: 1, Total: 3,
		Pomodoro: cfg.ActivePomodoro, Budgets: cfg.TimeBudgets, Windows: cfg.ScheduledWindows,
		ProjectTitles: titles, CaptureBucketID: "bucket", PriorityCount: 1,
		HideNonPriority: true, EnforceLocks: true, Now: focusViewNow,
	}
}

func focusNodeText(t *testing.T, root ui.ViewNode, id string) string {
	t.Helper()
	node := root.Find(id)
	if node == nil {
		t.Fatalf("node %q missing", id)
	}
	text, _ := node.Props["text"].(string)
	return text
}

func TestFocusCenterViewShowsBannerTableAndSummary(t *testing.T) {
	root := FocusCenterView(focusViewSpec())
	if root.ID != FocusCenterPageID || root.Scope != ScopeFocusCenter {
		t.Fatalf("page id %q scope %q", root.ID, root.Scope)
	}
	banner := focusNodeText(t, root, FocusBannerID)
	for _, want := range []string{"Parser", "18m left", "ends 12:18", "on Apollo", "other projects locked", "★ 1 priority today", "hidden in the outline"} {
		if !strings.Contains(banner, want) {
			t.Errorf("banner is missing %q: %q", want, banner)
		}
	}
	table := root.Find(FocusProjectsID)
	if table == nil || table.Kind != ui.KindTable || !table.Focusable || table.Filter == nil || table.Filter.ID != FocusProjectsID {
		t.Fatalf("the projects table does not declare a filter: %+v", table)
	}
	rows := table.Props["rows"].([]any)
	if len(rows) != 3 || table.Props["selected"] != 1 {
		t.Fatalf("rows %d selected %v", len(rows), table.Props["selected"])
	}
	apollo := rows[0].(ui.TableRowSpec).Cells
	if apollo[0] != "★ priority" || apollo[1] != "—" || apollo[3] != "Apollo  [PR Reviews 10:00]" {
		t.Fatalf("priority row cells: %v", apollo)
	}
	borealis := rows[1].(ui.TableRowSpec)
	lock := borealis.Cells[1].(string)
	if !strings.HasPrefix(lock, FocusLockMark+" Parser is running") || !strings.HasSuffix(lock, "until 12:18") || borealis.CellTones[1] != "warning" {
		t.Fatalf("lock cell %q tone %q", lock, borealis.CellTones[1])
	}
	if got := borealis.Cells[2]; got != "42m / 90m per 180m, 240m a day, cooldown 2 Pomodoros" {
		t.Fatalf("budget cell %q", got)
	}
	if bucket := rows[2].(ui.TableRowSpec).Cells; bucket[3] != data.CaptureBucketTitle+"  (capture bucket)" || bucket[1] != "—" {
		t.Fatalf("capture bucket cells: %v", bucket)
	}
	summary := focusNodeText(t, root, FocusSummaryID)
	for _, want := range []string{"Guardrails Borealis 90m per 180m", "Windows PR Reviews 10:00 45m on Apollo " + FocusLockMark, "Capture " + data.CaptureBucketTitle} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary is missing %q: %q", want, summary)
		}
	}
	want := map[string]any{"hasSelection": true, "projectId": "borealis", "pomodoroActive": true, "isPriority": false, "isLocked": true,
		"count": 3, "total": 3, "hasBudget": true, "hasWindow": false, "windowActive": false}
	for key, v := range want {
		if root.Facts[key] != v {
			t.Errorf("fact %s = %v, want %v", key, root.Facts[key], v)
		}
	}
}

func TestFocusCenterViewWithoutPomodoroShowsWindowsAndEmptyStates(t *testing.T) {
	spec := focusViewSpec()
	window := spec.Windows[0]
	spec.Pomodoro, spec.Window = nil, &window
	spec.Now = time.Date(2026, 11, 26, 10, 5, 0, 0, time.UTC)
	root := FocusCenterView(spec)
	if banner := focusNodeText(t, root, FocusBannerID); !strings.Contains(banner, "PR Reviews window open until 10:45") || !strings.Contains(banner, "other projects locked") {
		t.Fatalf("open window banner: %q", banner)
	}
	if root.Facts["pomodoroActive"] != false || root.Facts["windowActive"] != true {
		t.Fatalf("facts: %v", root.Facts)
	}

	window.ReportCommand = "make pr-report"
	spec.Window, spec.Pending = nil, &window
	if banner := focusNodeText(t, FocusCenterView(spec), FocusBannerID); !strings.Contains(banner, "PR Reviews window is due") || !strings.Contains(banner, "make pr-report") {
		t.Fatalf("pending window banner: %q", banner)
	}

	empty := FocusCenterView(FocusCenterSpec{SelectedIdx: -1, Now: focusViewNow, FilterQuery: "zzz"})
	if empty.Facts["hasSelection"] != false || empty.Facts["projectId"] != "" || empty.Facts["count"] != 0 || empty.Facts["hasFilter"] != true {
		t.Fatalf("empty facts: %v", empty.Facts)
	}
	if text := focusNodeText(t, empty, FocusProjectsID); !strings.Contains(text, "No projects match the filter") {
		t.Fatalf("empty table: %q", text)
	}
	if banner := focusNodeText(t, empty, FocusBannerID); !strings.Contains(banner, "no Pomodoro running") {
		t.Fatalf("idle banner: %q", banner)
	}
	if summary := focusNodeText(t, empty, FocusSummaryID); !strings.Contains(summary, "b sets one") || !strings.Contains(summary, "w adds one") || !strings.Contains(summary, "c creates the "+data.CaptureBucketTitle) {
		t.Fatalf("empty summary: %q", summary)
	}
}

func TestFocusCountdownRoundsUpToWholeMinutes(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0: "0m", 20 * time.Second: "<1m", time.Minute: "1m", 61 * time.Second: "2m", 25 * time.Minute: "25m", 65 * time.Minute: "1h 05m",
	} {
		if got := FocusCountdown(d); got != want {
			t.Errorf("FocusCountdown(%v) = %q, want %q", d, got, want)
		}
	}
}
