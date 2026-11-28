package app

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/pathpicker"
)

type focusFixture struct {
	t        *testing.T
	app      *App
	driver   *Driver
	now      time.Time
	dir      string
	apollo   data.Record
	borealis data.Record
}

func (f *focusFixture) mk(kind string, fields map[string]any) data.Record {
	f.t.Helper()
	rec, _, err := f.app.Store.Create(data.CreateInput{Kind: kind, Fields: fields}, User)
	if err != nil {
		f.t.Fatalf("create %s: %v", kind, err)
	}
	return *rec
}

func (f *focusFixture) fact(key string) any {
	f.t.Helper()
	root := f.app.View(f.app.State())
	node := root.Find(views.FocusCenterPageID)
	if node == nil {
		f.t.Fatal("Focus & Pomodoro Control Center missing from the rendered view")
	}
	return node.Facts[key]
}

// selectProject puts the table's selection on projectID with j/k.
func (f *focusFixture) selectProject(projectID string) {
	f.t.Helper()
	for _, key := range []string{"j", "k"} {
		for range 6 {
			if f.fact("projectId") == projectID {
				return
			}
			f.driver.Keys(key)
		}
	}
	f.t.Fatalf("could not select project %s (on %v)", projectID, f.fact("projectId"))
}

// pass moves the clock on and wakes the app, as the watcher's heartbeat does.
func (f *focusFixture) pass(d time.Duration) {
	f.now = f.now.Add(d)
	f.wake()
}

func (f *focusFixture) wake() {
	f.driver.Send(core.CommandEvent{Command: core.Command{ID: CmdWake, Source: "wake", Actor: core.Actor{Kind: "system", ID: "mtm.wake"}}})
}

func (f *focusFixture) open(projectID string) {
	f.driver.Send(core.CommandEvent{Command: core.Command{ID: CmdProjectTabOpen, Args: map[string]any{"project": projectID}, Actor: User}})
}

func (f *focusFixture) setting(name string, value any) {
	a := f.app
	a.cfg.userSettings[name] = value
	a.reloadSettings()
	a.applySettings()
}

func setupFocusFixture(t *testing.T) *focusFixture {
	t.Helper()
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	f := &focusFixture{t: t, now: time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC), dir: t.TempDir()}
	a, err := New(store, Options{FS: pathpicker.OSFS{}, Now: func() time.Time { return f.now }, DataDir: t.TempDir(), ConfigDir: f.dir})
	if err != nil {
		t.Fatal(err)
	}
	f.app, f.driver = a, NewDriver(a, 200, 40)
	f.apollo = f.mk(data.KindTask, map[string]any{"title": "Apollo"})
	f.borealis = f.mk(data.KindTask, map[string]any{"title": "Borealis"})
	f.mk(data.KindTask, map[string]any{"title": "Parser", "parent": f.apollo.Ref()})
	return f
}

func TestFocusCenterOpensOnGZAndTogglesPriorityProjects(t *testing.T) {
	f := setupFocusFixture(t)
	a, d := f.app, f.driver
	topTabs := a.topTabs()

	d.Keys("g", "Z")
	if a.Space() != SpaceFocus || a.Focus() != views.FocusProjectsID {
		t.Fatalf("g Z opened %q, focus %q", a.Space(), a.Focus())
	}
	if !slices.Equal(a.topTabs(), topTabs) {
		t.Fatal("opening the Focus & Pomodoro Control Center changed the numbered top tabs")
	}
	if f.fact("count") != 2 || f.fact("hasSelection") != true || f.fact("pomodoroActive") != false || f.fact("isPriority") != false {
		t.Fatalf("facts: count %v hasSelection %v", f.fact("count"), f.fact("hasSelection"))
	}
	frame := d.Frame()
	for _, want := range []string{"no Pomodoro running", "Apollo", "Borealis", "Guardrails", "Windows", "space/p priority today"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("the page is missing %q:\n%s", want, frame)
		}
	}

	// space and p toggle the selected project in today's priority projects.
	f.selectProject(f.borealis.ID)
	d.Keys("space")
	if f.fact("isPriority") != true || f.fact("priorityCount") != 1 || !data.IsPriorityProject(a.focus.cfg, f.borealis.ID, f.now) {
		t.Fatalf("space did not make Borealis a priority: %+v", a.focus.cfg)
	}
	// A priority project leads the table, and the state is in focus.json.
	if a.focus.visible[0].ProjectID != f.borealis.ID || !strings.Contains(d.Frame(), "★ priority") {
		t.Fatalf("the priority project does not lead the table: %+v", a.focus.visible)
	}
	saved, err := data.LoadFocusConfig(f.dir)
	if err != nil || !slices.Equal(saved.PriorityProjects, []string{f.borealis.ID}) || saved.PriorityDate != "2026-11-26" {
		t.Fatalf("focus.json: %+v, %v", saved, err)
	}
	d.Keys("p")
	if f.fact("isPriority") != false || f.fact("priorityCount") != 0 {
		t.Fatal("p did not take Borealis out of today's priorities")
	}

	// The table declares ViewNode.Filter, so / filters it through trait.filter.
	d.Keys("/")
	d.Type("apol")
	d.Keys("enter")
	if f.fact("hasFilter") != true || f.fact("count") != 1 || f.fact("total") != 2 || f.fact("projectId") != f.apollo.ID {
		t.Fatalf("/apol: hasFilter %v count %v total %v selected %v", f.fact("hasFilter"), f.fact("count"), f.fact("total"), f.fact("projectId"))
	}
}

func TestFocusBindingsAndCommandSpecs(t *testing.T) {
	var gZ []core.Binding
	for _, b := range DefaultBindings() {
		if b.Mode == ModeNormal && slices.Equal(b.Keys, keys("g Z")) {
			gZ = append(gZ, b)
		}
	}
	if len(gZ) != 1 || gZ[0].Command != CmdFocusCenterOpen || gZ[0].Scope != "" || gZ[0].Hint != "productivity & focus" {
		t.Fatalf("g Z bindings = %+v, want exactly the Focus & Pomodoro Control Center", gZ)
	}
	ids := map[string]bool{}
	for _, sp := range CommandSpecs() {
		ids[sp.ID] = true
	}
	bound := map[string]string{}
	for _, b := range focusBindings() {
		if !ids[b.Command] {
			t.Errorf("binding %v runs %q, which has no command spec", b.Keys, b.Command)
		}
		if b.Scope == views.ScopeFocusCenter {
			bound[strings.Join(b.Keys, " ")] = b.Command
		}
	}
	for key, cmd := range map[string]string{"space": cmdFocusPriorityToggle, "p": cmdFocusPriorityToggle, "s": cmdFocusPomodoroStart,
		"x": cmdFocusPomodoroStop, "b": cmdFocusBudgetEdit, "w": cmdFocusWindowEdit, "c": CmdFocusCaptureBucket} {
		if bound[key] != cmd {
			t.Errorf("%s runs %q on the Focus & Pomodoro Control Center, want %q", key, bound[key], cmd)
		}
	}
}

func TestFocusPriorityProjectsFilterTheTaskOutline(t *testing.T) {
	f := setupFocusFixture(t)
	a := f.app
	roots := func() []string {
		nodes, err := a.recordTreeNodes(data.KindTask)
		if err != nil {
			t.Fatal(err)
		}
		var titles []string
		for _, nd := range a.visibleTreeRoots(nodes, nil, data.KindTask) {
			titles = append(titles, nd.Record.Text("title"))
		}
		return titles
	}
	if got := roots(); !slices.Equal(got, []string{"Apollo", "Borealis"}) || a.focusOutlineBadges(data.KindTask) != nil {
		t.Fatalf("with no priority projects the outline shows %v", got)
	}
	keyOff := a.focusOutlineKey(data.KindTask)

	bucket, err := a.captureBucket()
	if err != nil {
		t.Fatal(err)
	}
	a.focusUpdate(data.TogglePriorityProject(a.focus.cfg, f.apollo.ID, f.now))
	if got := roots(); !slices.Equal(got, []string{"Apollo", data.CaptureBucketTitle}) {
		t.Fatalf("focus mode shows %v, want the priority project and the capture bucket (%s)", got, bucket.ID)
	}
	badges := a.focusOutlineBadges(data.KindTask)
	if len(badges) != 1 || !strings.Contains(badges[0].Text, "focus: 1 priority today") {
		t.Fatalf("the outline does not say focus mode is on: %+v", badges)
	}
	if a.focusOutlineKey(data.KindTask) == keyOff {
		t.Fatal("the outline's cache key ignores the priority filter")
	}
	// The outline overlay says so too, and shows only what the filter keeps.
	f.driver.Keys("2", "T")
	if frame := f.driver.Frame(); !strings.Contains(frame, "focus: 1 priority today") || !strings.Contains(frame, "Apollo") || strings.Contains(frame, "Borealis") {
		t.Fatalf("the outline in focus mode:\n%s", frame)
	}
	// A clean slate's own roots win over the priority filter.
	nodes, _ := a.recordTreeNodes(data.KindTask)
	cs := &cleanSlateState{active: true, roots: map[string]bool{f.borealis.ID: true}}
	if got := a.visibleTreeRoots(nodes, cs, data.KindTask); len(got) != 1 || got[0].Record.ID != f.borealis.ID {
		t.Fatalf("clean slate roots: %+v", got)
	}

	f.setting(SetFocusHideNonPriority, false)
	if got := roots(); len(got) != 3 || a.focusOutlineBadges(data.KindTask) != nil {
		t.Fatalf("focus.hideNonPriorityProjects=false still filters: %v", got)
	}
	// Tomorrow yesterday's priorities filter nothing.
	f.setting(SetFocusHideNonPriority, true)
	f.now = f.now.AddDate(0, 0, 1)
	if got := roots(); len(got) != 3 {
		t.Fatalf("yesterday's priorities still filter: %v", got)
	}
}

func TestFocusPomodoroLocksOtherProjectsAndShowsACountdown(t *testing.T) {
	f := setupFocusFixture(t)
	a, d := f.app, f.driver
	d.Keys("g", "Z")
	f.selectProject(f.apollo.ID)

	// s asks for the length and the projects; saving starts the Pomodoro.
	d.Keys("s")
	if a.Modal() != focusFormID {
		t.Fatalf("s did not open the Pomodoro form (modal %q)", a.Modal())
	}
	d.Keys("ctrl+s")
	p := a.focus.cfg.ActivePomodoro
	if a.Modal() != "" || p == nil || p.DurationMin != data.DefaultPomodoroMinutes || !slices.Equal(p.AllowedProjectIDs, []string{f.apollo.ID}) {
		t.Fatalf("the Pomodoro did not start on Apollo for 25 min (modal %q): %+v", a.Modal(), p)
	}
	if f.fact("pomodoroActive") != true {
		t.Fatal("pomodoroActive is false while a Pomodoro runs")
	}
	frame := d.Frame()
	for _, want := range []string{"25m left", "ends 12:25", "on Apollo", "🍅 25m", views.FocusLockMark + " Pomodoro is running"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("the running Pomodoro is missing %q:\n%s", want, frame)
		}
	}

	// A locked project is refused, with why and until when.
	f.open(f.borealis.ID)
	if a.Space() != SpaceFocus || !strings.Contains(a.status, "Borealis is locked") || !strings.Contains(a.status, "unlocks at 12:25") {
		t.Fatalf("opening a locked project: space %q, status %q", a.Space(), a.status)
	}
	if slices.ContainsFunc(a.topTabs(), func(tb topTabDef) bool { return tb.projectID == f.borealis.ID }) {
		t.Fatal("a refused project still got a tab")
	}
	f.selectProject(f.borealis.ID)
	d.Keys("enter")
	if a.Space() != SpaceFocus {
		t.Fatalf("enter opened the locked project (%q)", a.Space())
	}
	// The allowed project opens.
	f.open(f.apollo.ID)
	if a.Space() != projectTabID(f.apollo.ID) {
		t.Fatalf("the allowed project did not open (space %q, status %q)", a.Space(), a.status)
	}

	// focus.chromeCountdown and focus.enforceLocks are opt-outs.
	f.setting(SetFocusChromeCountdown, false)
	d.Keys("g", "Z")
	if strings.Contains(d.Frame(), "🍅") {
		t.Fatal("focus.chromeCountdown=false still shows the chip")
	}
	f.setting(SetFocusEnforceLocks, false)
	f.open(f.borealis.ID)
	if a.Space() != projectTabID(f.borealis.ID) {
		t.Fatalf("focus.enforceLocks=false still refuses (space %q)", a.Space())
	}
	f.setting(SetFocusEnforceLocks, true)
	f.setting(SetFocusChromeCountdown, true)

	// x stops it; time running out completes it.
	d.Keys("g", "Z")
	d.Keys("x")
	if a.focus.cfg.ActivePomodoro != nil || f.fact("pomodoroActive") != false {
		t.Fatal("x did not stop the Pomodoro")
	}
	f.selectProject(f.apollo.ID)
	d.Keys("s")
	d.Keys("ctrl+s")
	f.pass(24 * time.Minute)
	if !strings.Contains(d.Frame(), "🍅 1m") {
		t.Fatalf("the countdown did not follow the clock:\n%s", d.Frame())
	}
	f.pass(time.Minute)
	if a.focus.cfg.ActivePomodoro != nil || !strings.Contains(a.status, "Pomodoro complete") {
		t.Fatalf("the Pomodoro did not complete: %+v, status %q", a.focus.cfg.ActivePomodoro, a.status)
	}
	f.open(f.borealis.ID)
	if a.Space() != projectTabID(f.borealis.ID) {
		t.Fatal("projects stayed locked after the Pomodoro completed")
	}
}

func TestFocusTracksProjectMinutesAndLocksOverBudget(t *testing.T) {
	f := setupFocusFixture(t)
	a, d := f.app, f.driver
	a.focusUpdate(data.SetTimeBudget(a.focus.cfg, data.ProjectTimeBudget{ProjectID: f.apollo.ID, WindowMinutes: 3, RollingSpanMinutes: 60, CooldownPomodoros: 1}))

	f.open(f.apollo.ID)
	for range 2 {
		f.pass(time.Minute)
	}
	if got := data.TodayMinutes(a.focus.cfg, f.apollo.ID, f.now); got != 2 {
		t.Fatalf("2 minutes in Apollo logged %d min: %+v", got, a.focus.cfg.UsageLog)
	}
	// A stretch the app slept through is not work.
	f.pass(30 * time.Minute)
	if got := data.TodayMinutes(a.focus.cfg, f.apollo.ID, f.now); got != 2 {
		t.Fatalf("a 30 minute gap was credited: %d min", got)
	}
	d.Keys("j") // the owner is back
	f.pass(time.Minute)
	if len(a.focus.cfg.CooldownLocks) != 1 || !strings.Contains(a.status, "Apollo is locked until") {
		t.Fatalf("3 of 3 minutes did not lock Apollo: %+v, status %q", a.focus.cfg.CooldownLocks, a.status)
	}
	// Time on another page is not Apollo's.
	d.Keys("g", "Z")
	f.pass(time.Minute)
	if got := data.TodayMinutes(a.focus.cfg, f.apollo.ID, f.now); got != 3 {
		t.Fatalf("time on the focus page was credited to Apollo: %d min", got)
	}
	f.selectProject(f.apollo.ID)
	if f.fact("isLocked") != true || f.fact("hasBudget") != true || !strings.Contains(d.Frame(), "3m / 3m per 60m") {
		t.Fatalf("the locked, budgeted project on the page:\n%s", d.Frame())
	}
	f.open(f.apollo.ID)
	if a.Space() != SpaceFocus || !strings.Contains(a.status, "time budget used") {
		t.Fatalf("a project over budget opened (space %q, status %q)", a.Space(), a.status)
	}
	// The cooldown is one Pomodoro; then the project opens again.
	f.pass(data.DefaultPomodoroMinutes * time.Minute)
	f.open(f.apollo.ID)
	if a.Space() != projectTabID(f.apollo.ID) {
		t.Fatalf("the project stayed locked after its cooldown (status %q)", a.status)
	}
}

func TestFocusBudgetAndWindowForms(t *testing.T) {
	f := setupFocusFixture(t)
	a, d := f.app, f.driver
	d.Keys("g", "Z")
	f.selectProject(f.apollo.ID)

	d.Keys("b")
	fm := a.activeForm(focusFormID)
	if fm == nil {
		t.Fatalf("b did not open the time budget form (modal %q)", a.Modal())
	}
	fm.Model().SetValue("rollingMinutes", 90)
	fm.Model().SetValue("dailyMaxMinutes", 240)
	d.Keys("ctrl+s")
	if b, ok := data.TimeBudgetFor(a.focus.cfg, f.apollo.ID); !ok || b.WindowMinutes != 90 || b.DailyMaxMinutes != 240 || b.CooldownPomodoros != 1 {
		t.Fatalf("the time budget was not saved: %+v", a.focus.cfg.TimeBudgets)
	}
	if f.fact("hasBudget") != true {
		t.Fatal("hasBudget is false on a budgeted project")
	}

	// w adds the default PR Reviews window; W turns it off and on.
	d.Keys("w")
	if a.activeForm(focusFormID) == nil {
		t.Fatalf("w did not open the focus window form (modal %q)", a.Modal())
	}
	d.Keys("ctrl+s")
	w, ok := data.WindowFor(a.focus.cfg, f.apollo.ID, "PR Reviews")
	if !ok || w.DailyTimeHHMM != "10:00" || !w.LockOtherProjects || !w.Enabled || f.fact("hasWindow") != true {
		t.Fatalf("the PR Reviews window was not saved: %+v", a.focus.cfg.ScheduledWindows)
	}
	d.Keys("W")
	if w, _ := data.WindowFor(a.focus.cfg, f.apollo.ID, ""); w.Enabled {
		t.Fatal("W did not turn the window off")
	}
	// A time that is not HH:MM keeps the form open and says why.
	d.Keys("w")
	a.activeForm(focusFormID).Model().SetValue("time", "half past ten")
	d.Keys("ctrl+s")
	if a.Modal() != focusFormID || !strings.Contains(a.status, "not HH:MM") {
		t.Fatalf("a bad time was accepted (modal %q, status %q)", a.Modal(), a.status)
	}
}

func TestFocusScheduledWindowRunsItsReportThenLocksOtherProjects(t *testing.T) {
	f := setupFocusFixture(t)
	a := f.app
	var ran []string
	a.focus.runReport = func(_ context.Context, command string) error {
		ran = append(ran, command)
		return errors.New("exit 1")
	}
	cfg, err := data.UpsertWindow(a.focus.cfg, data.FocusWindowRule{
		Name: "PR Reviews", TargetProjectID: f.apollo.ID, DailyTimeHHMM: "12:30", DurationMin: 45,
		LockOtherProjects: true, Enabled: true, ReportCommand: "make pr-report",
	})
	if err != nil {
		t.Fatal(err)
	}
	a.focusUpdate(cfg)

	f.pass(29 * time.Minute)
	f.open(f.borealis.ID)
	if a.Space() != projectTabID(f.borealis.ID) || len(ran) != 0 {
		t.Fatalf("the window acted before its time (space %q, ran %v)", a.Space(), ran)
	}
	// 12:30: the window is due and its report command runs, once.
	f.pass(time.Minute)
	a.WaitBackground()
	f.wake()
	f.pass(10 * time.Second)
	if !slices.Equal(ran, []string{"make pr-report"}) {
		t.Fatalf("report command runs: %v", ran)
	}
	w, ok := data.ActiveScheduledWindow(a.focus.cfg, f.now)
	if !ok || !w.ReportReady || !strings.Contains(a.status, "PR Reviews window open on Apollo until 13:15") || !strings.Contains(a.status, "report command failed") {
		t.Fatalf("the window did not open after its report (ok %v, status %q): %+v", ok, a.status, a.focus.cfg.ScheduledWindows)
	}
	f.driver.Keys("g", "Z")
	if frame := f.driver.Frame(); !strings.Contains(frame, "PR Reviews window open until 13:15") || f.fact("windowActive") != true {
		t.Fatalf("the open window is not on the page:\n%s", frame)
	}
	f.open(f.borealis.ID)
	if a.Space() != SpaceFocus || !strings.Contains(a.status, "the PR Reviews window is open") {
		t.Fatalf("another project opened during the window (space %q, status %q)", a.Space(), a.status)
	}
	f.open(f.apollo.ID)
	if a.Space() != projectTabID(f.apollo.ID) {
		t.Fatal("the window's own project is locked")
	}
	// 13:15: the window closed.
	f.pass(45 * time.Minute)
	f.open(f.borealis.ID)
	if a.Space() != projectTabID(f.borealis.ID) {
		t.Fatalf("the window still locks after it closed (status %q)", a.status)
	}
}

func TestFocusCaptureFilesAnItemUnderTheCaptureBucket(t *testing.T) {
	f := setupFocusFixture(t)
	a, d := f.app, f.driver

	// From anywhere: the command needs no page.
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdFocusCaptureBucket, Actor: User}})
	if a.Modal() != focusCaptureFormID {
		t.Fatalf("the capture form did not open (modal %q)", a.Modal())
	}
	d.Type("renew the certificate")
	d.Keys("enter")
	bucketID := a.focus.cfg.CaptureBucketID
	if a.Modal() != "" || bucketID == "" {
		t.Fatalf("capture did not finish (modal %q, bucket %q, status %q)", a.Modal(), bucketID, a.status)
	}
	bucket, err := a.Store.Get(core.Ref{Kind: data.KindTask, ID: bucketID})
	if err != nil || bucket.Text("title") != data.CaptureBucketTitle {
		t.Fatalf("the capture bucket task: %+v, %v", bucket, err)
	}
	// c on the page captures into the same bucket.
	d.Keys("g", "Z")
	d.Keys("c")
	d.Type("call the plumber")
	d.Keys("enter")
	if a.focus.cfg.CaptureBucketID != bucketID {
		t.Fatal("a second capture made a second bucket")
	}
	res, err := a.Store.Query(data.Query{Kind: data.KindTask})
	if err != nil {
		t.Fatal(err)
	}
	var captured []string
	for _, rec := range res.Items {
		if parent, ok := rec.RefField("parent"); ok && parent.ID == bucketID {
			captured = append(captured, rec.Text("title"))
		}
	}
	slices.Sort(captured)
	if !slices.Equal(captured, []string{"call the plumber", "renew the certificate"}) {
		t.Fatalf("items under the capture bucket: %v", captured)
	}
	if saved, _ := data.LoadFocusConfig(f.dir); saved.CaptureBucketID != bucketID {
		t.Fatalf("focus.json does not name the capture bucket: %+v", saved)
	}
	if !strings.Contains(d.Frame(), data.CaptureBucketTitle+"  (capture bucket)") {
		t.Fatalf("the capture bucket is not marked on the page:\n%s", d.Frame())
	}
}
