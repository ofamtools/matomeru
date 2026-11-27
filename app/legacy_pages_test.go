package app

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

func enableLegacyPagesForTest(a *App, startSpace string) {
	a.cfg.userSettings[SetShowLegacyPanesWorkPages] = true
	a.reloadSettings()
	a.applySettings()
	if startSpace != "" {
		a.Shell.State = a.openSpace(a.State(), startSpace)
	}
}

func demoLegacy(t *testing.T) (*App, *Driver) {
	t.Helper()
	a, d := demo(t)
	enableLegacyPagesForTest(a, SpacePanes)
	return a, d
}

func topTabIDs(a *App) []string {
	tabs := a.topTabs()
	ids := make([]string, len(tabs))
	for i, tb := range tabs {
		ids[i] = tb.id
	}
	return ids
}

func TestLegacyPanesWorkPagesHiddenByDefaultAndKeepActions(t *testing.T) {
	rep, err := CheckBindings("")
	if err != nil {
		t.Fatalf("CheckBindings error: %v", err)
	}
	if len(rep.Errors) != 0 {
		t.Fatalf("CheckBindings reported %d error(s): %+v", len(rep.Errors), rep.Errors)
	}

	a, d := demo(t)

	// 1. Default setting is false; startup lands on Projects (never hidden Panes/Work).
	if a.showLegacyPanesWorkPages() {
		t.Fatalf("%s defaulted to true, want false", SetShowLegacyPanesWorkPages)
	}
	if a.Space() != SpaceProjects || a.Focus() != projectsDirTable {
		t.Fatalf("default startup = (%q, %q), want (%q, %q)", a.Space(), a.Focus(), SpaceProjects, projectsDirTable)
	}

	// 2. Top tab strip omits Panes and Work; Projects leads followed by Runs, Extensions and Reminders.
	wantIDs := []string{SpaceProjects, SpaceRuns, SpaceExtensions, SpaceReminders}
	if got := topTabIDs(a); !slices.Equal(got, wantIDs) {
		t.Fatalf("default topTabs = %v, want %v", got, wantIDs)
	}
	frame := d.Frame()
	topLine := strings.Split(frame, "\n")[0]
	for _, want := range []string{"Projects", "Runs", "Extensions"} {
		if !strings.Contains(topLine, want) {
			t.Fatalf("top tab bar missing %q: %q", want, topLine)
		}
	}
	if strings.Contains(topLine, "Panes") || strings.Contains(topLine, "Work") {
		t.Fatalf("default top tab bar leaked legacy Panes/Work: %q", topLine)
	}

	// 3. Numbered shortcuts (1, 2, 3) and [ / ] cycle only visible tabs.
	d.Keys("2")
	if a.Space() != SpaceRuns {
		t.Fatalf("2 -> %q, want %q", a.Space(), SpaceRuns)
	}
	d.Keys("3")
	if a.Space() != SpaceExtensions {
		t.Fatalf("3 -> %q, want %q", a.Space(), SpaceExtensions)
	}
	d.Keys("]")
	if a.Space() != SpaceReminders {
		t.Fatalf("] from Extensions -> %q, want %q", a.Space(), SpaceReminders)
	}
	d.Keys("]")
	if a.Space() != SpaceProjects {
		t.Fatalf("] from Reminders -> %q, want %q", a.Space(), SpaceProjects)
	}
	d.Keys("[")
	if a.Space() != SpaceReminders {
		t.Fatalf("[ from Projects -> %q, want %q", a.Space(), SpaceReminders)
	}
	d.Keys("1")
	if a.Space() != SpaceProjects {
		t.Fatalf("1 -> %q, want %q", a.Space(), SpaceProjects)
	}

	// 4. Numbered tab strip (ui.topChromeRedesign=false) renders 1 Projects, 2 Runs, 3 Extensions.
	setSettingForTest(t, a, SetTopChromeRedesign, false)
	d.Send(resize(120, 36))
	numberedTop := strings.Split(d.Frame(), "\n")[0]
	for _, want := range []string{"1 Projects", "2 Runs", "3 Extensions"} {
		if !strings.Contains(numberedTop, want) {
			t.Fatalf("numbered top bar missing %q: %q", want, numberedTop)
		}
	}
	if strings.Contains(numberedTop, "Panes") || strings.Contains(numberedTop, "Work") {
		t.Fatalf("numbered top bar leaked legacy Panes/Work: %q", numberedTop)
	}
	setSettingForTest(t, a, SetTopChromeRedesign, true)

	// 5. Keep Actions: Panes and Work remain reachable via I, palette, CmdPageOpen, and ' picker,
	// and settle() never kicks the user off an explicitly opened legacy space.
	d.Keys("I")
	if a.Space() != SpacePanes {
		t.Fatalf("I did not open legacy Panes: space=%q", a.Space())
	}
	if !strings.Contains(d.Frame(), "claude: wave 5B") {
		t.Fatalf("explicitly opened Panes did not render rail:\n%s", d.Frame())
	}
	// A subsequent command (motion) stays on Panes (settle must not kick it away).
	d.Keys("j")
	if a.Space() != SpacePanes {
		t.Fatalf("command inside explicitly opened Panes kicked space to %q", a.Space())
	}

	// Palette still offers space.panes and space.work.
	d.Keys(":")
	d.Type("space.work")
	d.Keys("enter")
	if a.Space() != SpaceWork {
		t.Fatalf("palette space.work -> space=%q, want %q", a.Space(), SpaceWork)
	}
	if !strings.Contains(d.Frame(), "ochazuke-v2") {
		t.Fatalf("explicitly opened Work did not render tasks:\n%s", d.Frame())
	}
	d.Keys("j")
	if a.Space() != SpaceWork {
		t.Fatalf("command inside explicitly opened Work kicked space to %q", a.Space())
	}

	// Direct CmdPageOpen with id=SpacePanes still works.
	d.Send(core.CommandEvent{Command: core.Command{
		ID:    core.CmdPageOpen,
		Args:  map[string]any{"id": SpacePanes},
		Actor: User,
	}})
	if a.Space() != SpacePanes {
		t.Fatalf("CmdPageOpen id=panes -> space=%q, want %q", a.Space(), SpacePanes)
	}

	// Unified ' Pages picker still lists Panes and Work (badged legacy) and opens them.
	d.Keys("1", "'")
	pm := a.activePicker("pages")
	if pm == nil {
		t.Fatalf("' did not open pages picker: modal=%q", a.Modal())
	}
	hasLegacyWork := false
	for _, it := range pm.Model().Items {
		if it.ID == pagePickAppPrefix+"space:"+SpaceWork {
			hasLegacyWork = true
			for _, b := range it.Badges {
				if b.Text == "2" {
					t.Fatalf("hidden legacy Work should not claim slot 2 badge: %+v", it.Badges)
				}
			}
		}
	}
	if !hasLegacyWork {
		t.Fatal("' pages picker missing legacy Work entry")
	}
	d.Keys("esc", "esc")
}

func TestLegacyPanesWorkPagesOptInAndStartupFallback(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "matomeru-next")
	a, d := configured(t, dir)

	// Toggle ui.showLegacyPanesWorkPages=true via Settings and verify live update + persistence.
	setSettingForTest(t, a, SetShowLegacyPanesWorkPages, true)
	if !a.showLegacyPanesWorkPages() {
		t.Fatalf("%s did not turn on", SetShowLegacyPanesWorkPages)
	}
	wantLegacy := []string{SpacePanes, SpaceWork, SpaceRuns, SpaceExtensions, SpaceProjects, SpaceReminders}
	if got := topTabIDs(a); !slices.Equal(got, wantLegacy) {
		t.Fatalf("opt-in topTabs = %v, want %v", got, wantLegacy)
	}
	d.Keys("1")
	if a.Space() != SpacePanes {
		t.Fatalf("opt-in 1 -> %q, want %q", a.Space(), SpacePanes)
	}
	d.Keys("2")
	if a.Space() != SpaceWork {
		t.Fatalf("opt-in 2 -> %q, want %q", a.Space(), SpaceWork)
	}
	d.Keys("3")
	if a.Space() != SpaceRuns {
		t.Fatalf("opt-in 3 -> %q, want %q", a.Space(), SpaceRuns)
	}
	d.Keys("4")
	if a.Space() != SpaceExtensions {
		t.Fatalf("opt-in 4 -> %q, want %q", a.Space(), SpaceExtensions)
	}
	d.Keys("5")
	if a.Space() != SpaceProjects {
		t.Fatalf("opt-in 5 -> %q, want %q", a.Space(), SpaceProjects)
	}

	// Persist to settings.json and verify a fresh App boots onto SpacePanes with legacy tabs.
	if _, err := a.writeConfig(settingsFileName, a.cfg.userSettings); err != nil {
		t.Fatalf("writeConfig: %v", err)
	}
	reloaded, _ := configured(t, dir)
	if !reloaded.showLegacyPanesWorkPages() {
		t.Fatalf("reloaded %s = false, want true", SetShowLegacyPanesWorkPages)
	}
	if reloaded.Space() != SpacePanes {
		t.Fatalf("reloaded opt-in startup space = %q, want %q", reloaded.Space(), SpacePanes)
	}
	if got := topTabIDs(reloaded); !slices.Equal(got, wantLegacy) {
		t.Fatalf("reloaded opt-in topTabs = %v, want %v", got, wantLegacy)
	}

	// Edge case: ui.showLegacyPanesWorkPages=false AND projects.topNavTab.enabled=false
	// must land on Runs (first visible tab), never hidden Panes.
	dirNoProj := filepath.Join(t.TempDir(), "matomeru-noproj")
	a2, _ := configured(t, dirNoProj)
	a2.cfg.userSettings[SetProjectsTopNavTab] = false
	if _, err := a2.writeConfig(settingsFileName, a2.cfg.userSettings); err != nil {
		t.Fatalf("writeConfig no-proj: %v", err)
	}
	reloadedNoProj, _ := configured(t, dirNoProj)
	if reloadedNoProj.Space() != SpaceRuns {
		t.Fatalf("no-projects default startup = %q, want %q", reloadedNoProj.Space(), SpaceRuns)
	}
	if got := topTabIDs(reloadedNoProj); !slices.Equal(got, []string{SpaceRuns, SpaceExtensions, SpaceReminders}) {
		t.Fatalf("no-projects topTabs = %v, want [runs extensions reminders]", got)
	}
	// Create a task and verify dynamic tab slot numbering starts cleanly after visible tabs.
	proj, _, err := reloadedNoProj.Store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "SlotCheck"},
	}, User)
	if err != nil {
		t.Fatal(err)
	}
	reloadedNoProj.ensureProjectTabOpen(proj.ID)
	if got := topTabIDs(reloadedNoProj); !slices.Equal(got, []string{SpaceRuns, SpaceExtensions, projectTabID(proj.ID), SpaceReminders}) {
		t.Fatalf("no-projects + dynamic tab topTabs = %v", got)
	}
}
