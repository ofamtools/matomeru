//go:build monogatari

// The mtm tester (make story-bin): mtm's stories (cmd/mtm/stories) on
// monogatari's host, mounted as `mtm story`. Only a build with -tags
// monogatari has it; story_off.go is the normal build's empty mount, so
// the shipped binary links no monogatari.
//
// Every session is the real App (New) on a temp database, a temp config
// directory and a socket nothing listens on, with the fakes of
// storyfakes.go for the daemon, the launcher, the renamer and the system
// actions: a story never reaches the owner's data, daemon or tuios
// session. The reference is o-json's story.go.

package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/unixsuperhero/monogatari"
	"github.com/unixsuperhero/monogatari/host"
	"github.com/unixsuperhero/ochazuke"
	"github.com/unixsuperhero/oden"

	"github.com/unixsuperhero/matomeru/internal/actionitems"
	"github.com/unixsuperhero/matomeru/internal/compose"
	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/matomeru/store"
)

//go:embed stories
var storyFiles embed.FS

// storyProg is the host's command line in usage and messages; its slug,
// mtm-story, names the host's own state ($XDG_CONFIG_HOME/mtm-story,
// $XDG_DATA_HOME/mtm-story).
const storyProg = "mtm story"

// storyShell runs `story ...` before mtm opens any database: the words
// that start with story run the host as an oden command named story, and
// mtm's own options (-data, -socket, -server) are never read for it.
// handled is false for any other words.
func storyShell(words []string, stdin io.Reader, stdout, stderr io.Writer) (handled bool, err error) {
	if len(words) == 0 || words[0] != "story" {
		return false, nil
	}
	app := oden.New("mtm", words, oden.WithBuiltins(), oden.WithStdio(stdin, stdout, stderr))
	for _, c := range storyCommands() {
		app.Add(c)
	}
	return true, app.Run(context.Background())
}

// storyCommands is the tester's story command (host.Command).
func storyCommands() []oden.Command {
	return []oden.Command{host.Command(storyConfig())}
}

// storyConfig is mtm's host: one app, mtm, over the embedded stories.
func storyConfig() host.Config {
	stories, err := fs.Sub(storyFiles, "stories")
	if err != nil {
		panic(err) // the embed always has stories
	}
	return host.Config{Prog: storyProg, Apps: []monogatari.App{mtmApp()}, Stories: stories}
}

// mtmApp is mtm for stories: the real App on a temp database, seeded or
// empty, with faked panes.
func mtmApp() monogatari.App {
	return monogatari.App{
		Name:    "mtm",
		Summary: "mtm on a temp database and a faked daemon (seed panes: the fake panes)",
		Fixtures: []monogatari.Fixture{
			{Name: "seeded", Summary: "docs/vhs/seed.sh's projects, tasks and notes (acme-web, billing-api, docs-site), two inbox items and two fake panes; opens on Panes", Build: buildSeeded},
			{Name: "empty", Summary: "a fresh database and no panes, as a first start opens", Build: buildEmpty},
		},
		Expected: storyExpected,
	}
}

// storyExpected is an error the fakes mean: a story has no daemon.
func storyExpected(_, err string) bool {
	return strings.Contains(err, errStoryDaemon.Error()) || strings.Contains(err, storySocket)
}

// storySocket is the socket name a session's daemon client dials; nothing
// listens on it.
const storySocket = "no-daemon.sock"

// storyStart is when every story session starts: the inbox tests'
// clock. mtm reads a zero time as unset (an item read at 0 is unread),
// so the session's clock is this plus the driver's, which starts at the
// Unix epoch.
var storyStart = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

// storyClock is the session's clock for mtm: storyStart moved on by the
// driver's fake clock (Env.Now), so a story reads the same times every run.
func storyClock(env monogatari.Env) func() time.Time {
	return func() time.Time { return storyStart.Add(env.Now().Sub(time.Unix(0, 0))) }
}

// storySeed is the app data a story hands its fixture (its seed field).
type storySeed struct {
	// Panes replace the fixture's fake panes; [] is none.
	Panes *[]storyPane `json:"panes"`
}

// storyPane is one fake pane: its session, window id, name and screen.
type storyPane struct {
	Session string `json:"session"`
	Window  string `json:"window"`
	Name    string `json:"name"`
	Screen  string `json:"screen"`
}

func seedOf(env monogatari.Env) (storySeed, error) {
	var seed storySeed
	if len(env.Seed) > 0 {
		dec := json.NewDecoder(strings.NewReader(string(env.Seed)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&seed); err != nil {
			return seed, fmt.Errorf("seed: %w", err)
		}
	}
	return seed, nil
}

// seededPanes are the seeded fixture's panes: an agent and a shell, after
// the test fakes' (newFakePanes), in seed.sh's projects.
var seededPanes = []storyPane{
	{Session: "acme-web", Window: "w1", Name: "claude", Screen: "claude is thinking"},
	{Session: "billing-api", Window: "w2", Name: "shell", Screen: "$ go test ./..."},
}

func buildSeeded(env monogatari.Env) (monogatari.Built, error) {
	return buildStory(env, writeSeeded, seededPanes)
}

func buildEmpty(env monogatari.Env) (monogatari.Built, error) {
	return buildStory(env, nil, nil)
}

// buildStory makes one session: a temp dir holding the database (written
// by fill), the config and the dead socket; the App built on it; the
// fakes installed. Close closes the App and removes the dir.
func buildStory(env monogatari.Env, fill func(path string, now int64) error, panes []storyPane) (built monogatari.Built, err error) {
	seed, err := seedOf(env)
	if err != nil {
		return built, err
	}
	if seed.Panes != nil {
		panes = *seed.Panes
	}
	dir, err := os.MkdirTemp("", "mtm-story-")
	if err != nil {
		return built, err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(dir)
		}
	}()
	data := filepath.Join(dir, "mtm.db")
	now := storyClock(env)
	if fill != nil {
		if err := fill(data, now().UnixMilli()); err != nil {
			return built, fmt.Errorf("seed the database: %w", err)
		}
	}
	config := env.ConfigDir
	if config == "" {
		config = filepath.Join(dir, "config")
		if err := os.Mkdir(config, 0o755); err != nil {
			return built, err
		}
	}
	a, err := New(Options{Data: data, Config: config, Socket: filepath.Join(dir, storySocket)})
	if err != nil {
		return built, err
	}
	if err := sandboxStory(a, env, now, dir, panes); err != nil {
		a.Close()
		return built, err
	}
	// The pane list is loaded before the first frame, as the listing a
	// panes.refresh job delivers (refreshPanes), so a story and the
	// explorer start on the panes; a later refresh is a job event.
	listed, err := sideListing(context.Background(), a.panesd, a.work, a.server())
	a.setPanes(listed, err)
	a.m.Width, a.m.Height = env.Width, env.Height
	a.View()
	return monogatari.Built{
		Model:   a.m,
		Program: a,
		Close:   func() { a.Close(); os.RemoveAll(dir) },
	}, nil
}

// sandboxStory installs the fakes on a built App, after openWith's list
// (main_test.go): the story daemon, launcher and renamer; no timers; no
// environment, home, session logs or process table of the machine; the
// status segments off (no live readings); the story's settings and
// values (Env.Sandbox); and a foreground program never runs.
func sandboxStory(a *App, env monogatari.Env, now func() time.Time, dir string, panes []storyPane) error {
	home := filepath.Join(dir, "home")
	for _, d := range []string{home, filepath.Join(dir, "claude"), filepath.Join(dir, "omp"), filepath.Join(dir, "codex")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	var fakes []pane
	screens := map[string]string{}
	for _, p := range panes {
		fakes = append(fakes, pane{Session: p.Session, Pane: daemon.Pane{ID: p.Window, Name: p.Name}})
		screens[p.Window] = p.Screen
	}
	sp := newStoryPanes(fakes, screens)
	a.panesd, a.renamer, a.launcher = sp, sp, storyLauncher{}
	a.ensure = func(context.Context) error { return nil }
	a.home = func() (string, error) { return home, nil }
	a.getenv = func(string) string { return "" } // a story may run inside a TUIOS pane
	a.journalSrc = nil
	a.locator = nil
	a.agentStarted = func(pane) time.Time { return time.Time{} }
	a.claudeProjects = filepath.Join(dir, "claude")
	a.ompSessions = filepath.Join(dir, "omp")
	a.codexSessions = filepath.Join(dir, "codex")
	a.pb.sources = compose.NewSources(compose.Env{Home: home, ClaudeDir: filepath.Join(home, ".claude"), OmpDir: filepath.Join(home, ".omp", "agent")})
	a.pb.clip = storyClipboard{}
	a.cx.sys, a.inbox.sys = storySystem{}, storySystem{}
	a.sleep = func(context.Context, time.Duration) error { return nil }
	// The clock is the session's (storyClock), so times shown read the
	// same every run.
	a.clock, a.loc = now, time.UTC
	a.starts.now = now
	a.inbox.items.SetClock(func() int64 { return now().UnixMilli() })
	// No timers: a story's ticks are the driver's, not mtm's refreshes.
	a.pv.after, a.runs.after, a.sched.after, a.inbox.after = nil, nil, nil, nil
	a.sched.runner.Exec = func(context.Context, []string, []string) ([]byte, error) {
		return nil, errors.New("stories run no real commands")
	}
	for _, s := range a.r.Segments() {
		if err := a.r.AddSegment(ochazuke.Segment{ID: s.ID, Enabled: ochazuke.On(false)}); err != nil {
			return err
		}
	}
	if err := env.Sandbox(a.m); err != nil {
		return err
	}
	if err := a.refreshInbox(); err != nil { // its rows' times, on the story clock
		return err
	}
	// Env.Sandbox runs a foreground program headless; a story runs none
	// ($EDITOR, a pager), so nothing outside the session starts.
	a.m.Foreground = func(msg ochazuke.ForegroundMsg) tea.Cmd {
		return func() tea.Msg { return msg.Done(errors.New("stories run no foreground programs")) }
	}
	return nil
}

// writeSeeded writes docs/vhs/seed.sh's data into a new database: three
// projects, twelve tasks and three notes, plus two inbox items after the
// inbox tests' seedInbox (one unread with a URL in acme-web, one read
// with no owner). Ids are fixed so stories can name rows.
func writeSeeded(path string, now int64) error {
	db, err := store.Open(path)
	if err != nil {
		return err
	}
	defer db.Close()
	w, err := work.Open(db)
	if err != nil {
		return err
	}
	projects := []work.Project{
		{ID: "acme-web", Name: "acme-web", Theme: work.Theme{Color: "#7FB685", Glyph: "◆"}},
		{ID: "billing-api", Name: "billing-api", Theme: work.Theme{Color: "#E5A55D", Glyph: "◆"}},
		{ID: "docs-site", Name: "docs-site", Theme: work.Theme{Color: "#7AA2F7", Glyph: "◆"}},
	}
	for i, p := range projects {
		p.Created = int64(i + 1)
		if err := w.PutProject(p); err != nil {
			return err
		}
	}
	tasks := []struct{ project, status, title string }{
		{"acme-web", work.StatusActive, "Checkout page: show the shipping estimate"},
		{"acme-web", work.StatusTodo, "Move the cart drawer to the new design tokens"},
		{"acme-web", work.StatusBlocked, "Upgrade the image CDN (waiting on billing-api)"},
		{"acme-web", work.StatusTodo, "Flaky test: login redirect on Safari"},
		{"acme-web", work.StatusDone, "Dark mode for the order history"},
		{"billing-api", work.StatusActive, "Retry failed webhooks with backoff"},
		{"billing-api", work.StatusTodo, "Invoice PDF: add the tax id line"},
		{"billing-api", work.StatusTodo, "Rate limit the refunds endpoint"},
		{"billing-api", work.StatusDone, "Drop the legacy v1 coupons table"},
		{"docs-site", work.StatusTodo, "Write the quickstart for the CLI"},
		{"docs-site", work.StatusActive, "Search: index the API reference"},
		{"docs-site", work.StatusDone, "Fix broken links on the changelog page"},
	}
	for i, t := range tasks {
		task := work.Task{ID: fmt.Sprintf("t%d", i+1), ProjectID: t.project, Title: t.title, Status: t.status, Position: int64(i + 1), Created: int64(i + 1)}
		if err := w.PutTask(task); err != nil {
			return err
		}
	}
	notes := []work.Note{
		{ID: "n1", ProjectID: "acme-web", Text: "# Checkout release\n\nShip behind the `checkout_v2` flag, then ramp to 100%.\n\n- [x] Shipping estimate API wired up\n- [x] Copy reviewed\n- [ ] Screenshot tests on mobile\n- [ ] Ramp the flag to 25%\n- [ ] Announce in #web"},
		{ID: "n2", ProjectID: "billing-api", Text: "# Webhook retries\n\nBackoff: 1m, 5m, 30m, 2h, then give up and alert.\n\n- [x] Store attempt count per delivery\n- [ ] Dead-letter table\n- [ ] Dashboard for stuck deliveries"},
		{ID: "n3", ProjectID: "docs-site", Text: "# Docs ideas\n\n- A page per command, generated from the catalog\n- Copy buttons on every snippet"},
	}
	for i, n := range notes {
		n.Created, n.Updated = int64(i+1), int64(i+1)
		if err := w.PutNote(n); err != nil {
			return err
		}
	}
	items, err := actionitems.Open(db)
	if err != nil {
		return err
	}
	clock := now
	items.SetClock(func() int64 { return clock })
	if _, _, err := items.Add(actionitems.Spec{Source: "gh", URL: "https://github.com/acme/web/pull/9", Title: "Checkout: shipping estimate", Why: "review requested", ProjectID: "acme-web"}); err != nil {
		return err
	}
	clock++
	ping, _, err := items.Add(actionitems.Spec{Source: "slack", Why: "mentioned", Title: "standup notes"})
	if err != nil {
		return err
	}
	_, err = items.SetRead(ping.ID, true)
	return err
}
