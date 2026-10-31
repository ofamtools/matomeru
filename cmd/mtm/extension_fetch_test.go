package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/unixsuperhero/ochazuke"
)

// catalogCalls counts the catalog execs in a fake tool's log.
func catalogCalls(t *testing.T, dir, name string) int {
	t.Helper()
	n := 0
	for _, c := range toolCalls(t, dir, name) {
		if c == "catalog" {
			n++
		}
	}
	return n
}

// Discovery always checks registered catalogs. Unregistered tools keep
// their stamp cache, while an explicit refresh always asks.
func TestDiscoveryChecksRegisteredCatalogs(t *testing.T) {
	dir := t.TempDir()
	alpha := fakeTool(t, dir, "alpha", toolCatalog("alpha", "1.0"))
	fakeTool(t, dir, "beta", toolCatalog("beta", "1.0"))
	path := seeded(t)
	mustCLI(t, openAt(t, path), "extension", "register", "alpha", "--bin", alpha)
	e, err := (extRegistry{Dir: filepath.Join(filepath.Dir(path), extensionsDir)}).Read("alpha")
	if err != nil || e.BinStamp == nil || e.BinStamp.Size == 0 {
		t.Fatalf("register wrote no binStamp: %+v %v", e.BinStamp, err)
	}
	a := extPageApp(t, path, dir)
	if extStatus(a, "alpha") != statusRegistered || extStatus(a, "beta") != statusAvailable {
		t.Fatalf("rows %+v", a.extList.Items())
	}
	if n, m := catalogCalls(t, dir, "alpha"), catalogCalls(t, dir, "beta"); n != 2 || m != 1 {
		t.Fatalf("first discovery: alpha asked %d times (register and check), beta %d (once)", n, m)
	}
	for range 2 {
		openExtPage(t, a, "work")
		openExtPage(t, a, extensionsPage)
	}
	if n, m := catalogCalls(t, dir, "alpha"), catalogCalls(t, dir, "beta"); n != 4 || m != 1 {
		t.Fatalf("reopening: alpha %d, want 4; beta %d, want 1", n, m)
	}
	if extStatus(a, "alpha") != statusRegistered || extStatus(a, "beta") != statusAvailable {
		t.Fatalf("rows from the cache %+v", a.extList.Items())
	}
	rebuilt(t, alpha)
	rebuilt(t, filepath.Join(dir, "o-beta"))
	openExtPage(t, a, "work")
	openExtPage(t, a, extensionsPage)
	if n, m := catalogCalls(t, dir, "alpha"), catalogCalls(t, dir, "beta"); n != 5 || m != 2 {
		t.Fatalf("after a rebuild: alpha asked %d times (want 5), beta %d (want 2)", n, m)
	}
	mustCLI(t, openAt(t, path), "extension", "refresh", "alpha")
	if n := catalogCalls(t, dir, "alpha"); n != 6 {
		t.Fatalf("refresh asked alpha %d times in all, want 6", n)
	}
}

// step runs cmd's jobs one round: each completion goes through Update,
// and the commands that started are returned unrun.
func step(t *testing.T, a *App, cmd tea.Cmd) []tea.Cmd {
	t.Helper()
	if cmd == nil {
		return nil
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-ch:
	case <-time.After(5 * time.Second):
		return nil // a timer
	}
	switch msg := msg.(type) {
	case tea.BatchMsg:
		var out []tea.Cmd
		for _, c := range msg {
			out = append(out, step(t, a, c)...)
		}
		return out
	case ochazuke.Completion:
		_, next := a.Update(msg)
		return []tea.Cmd{next}
	}
	return nil
}

// While anything fetches for an extension its row on the Extensions page
// says so in the status column: its catalog (discovery's probe of a
// changed bin, L), its rows or pick lists (its page's loads), then the
// status again, or the failure.
func TestExtensionRowShowsItsFetches(t *testing.T) {
	dir := t.TempDir()
	alpha := fakeTool(t, dir, "alpha", toolCatalog("alpha", "1.0"))
	path := seeded(t)
	mustCLI(t, openAt(t, path), "extension", "register", "alpha", "--bin", alpha)
	a := extPageApp(t, path, dir)
	if got := extStatus(a, "alpha"); got != statusRegistered {
		t.Fatalf("status %q", got)
	}
	// Its rows load (as opening its page does).
	a.reloadExtension(a.extension("alpha"))
	_, cmd := a.Update(nil)
	if got := extStatus(a, "alpha"); got != cellLoadingRows {
		t.Fatalf("while its rows load: %q", got)
	}
	settleJobs(t, a, tea.Batch(cmd, a.r.PendingCommands()))
	if got := extStatus(a, "alpha"); got != statusRegistered {
		t.Fatalf("after the rows landed: %q", got)
	}
	// L reads its catalog.
	if err := a.r.SetFocus(extListID + "/alpha"); err != nil {
		t.Fatal(err)
	}
	_, cmd = a.Update(keyMsg("L"))
	if got := extStatus(a, "alpha"); got != cellFetchingCatalog {
		t.Fatalf("while L reads the catalog: %q", got)
	}
	settleJobs(t, a, cmd)
	openExtPage(t, a, extensionsPage)
	if got := extStatus(a, "alpha"); got != statusRegistered {
		t.Fatalf("after L: %q", got)
	}
	// Discovery probes a rebuilt bin: fetching while the probe runs.
	rebuilt(t, alpha)
	openExtPage(t, a, "work")
	if err := a.r.OpenPage(extensionsPage); err != nil {
		t.Fatal(err)
	}
	_, cmd = a.Update(nil)
	probing := step(t, a, cmd) // the plan landed; the probe runs
	if got := extStatus(a, "alpha"); got != cellFetchingCatalog {
		t.Fatalf("while discovery asks the rebuilt bin: %q", got)
	}
	for _, c := range probing {
		settleJobs(t, a, c)
	}
	if got := extStatus(a, "alpha"); got != statusRegistered {
		t.Fatalf("after the probe: %q", got)
	}
	// Its rows fail to load: the row says why until the next fetch.
	script, err := os.ReadFile(alpha)
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(string(script), "item) echo", `item) echo "rows broke" >&2; exit 3; echo`, 1)
	if err := os.WriteFile(alpha, []byte(broken), 0o755); err != nil {
		t.Fatal(err)
	}
	a.reloadExtension(a.extension("alpha"))
	_, cmd = a.Update(nil)
	settleJobs(t, a, tea.Batch(cmd, a.r.PendingCommands()))
	a.Update(nil)
	if got := extStatus(a, "alpha"); !strings.HasPrefix(got, "error: ") || !strings.Contains(got, "rows broke") {
		t.Fatalf("after a failed load: %q", got)
	}
}
