package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/harness"
	"github.com/unixsuperhero/matomeru/internal/store"
	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
)

// Real Neovim, with a deterministic configuration; only the daemon transport
// is substituted. The same argv the daemon would run is used headlessly.
type vimProcessDriver struct {
	t         *testing.T
	panes     *fakePanes
	commands  [][]string
	processes []*exec.Cmd
}

func (d *vimProcessDriver) OpenWindow(_ context.Context, w harness.Window) (string, error) {
	args := append([]string{"--headless", "-u", "NONE", "-i", "NONE", "-n"}, w.Command[1:]...)
	cmd := exec.Command(w.Command[0], args...)
	cmd.Dir = w.Cwd
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		return "", err
	}
	d.processes = append(d.processes, cmd)
	d.commands = append(d.commands, slices.Clone(w.Command))
	d.t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait(); _ = os.RemoveAll(filepath.Dir(w.Command[2])) })
	id := fmt.Sprintf("vim-pane-%d", len(d.commands))
	d.panes.panes = append(d.panes.panes, pane{Session: w.Session, Pane: daemon.Pane{ID: id, Name: w.Name, Cwd: w.Cwd}})
	return id, nil
}
func (*vimProcessDriver) WaitReady(context.Context, string, string, harness.Ready) error { return nil }
func (*vimProcessDriver) SendText(context.Context, string, string, string) error {
	return fmt.Errorf("must never type save keys into Neovim")
}

func vimRPCForTest(t *testing.T, binary, socket, expr string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, binary, "--server", socket, "--remote-expr", expr).CombinedOutput()
	if err != nil {
		t.Fatalf("RPC: %v: %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestVimRealSaveRestoreRestartAndForget(t *testing.T) {
	binary, err := exec.LookPath("nvim")
	if err != nil {
		t.Skip("nvim not installed; real Neovim acceptance unverified")
	}
	root := t.TempDir()
	dbPath := filepath.Join(root, "mtm.db")
	a := openAt(t, dbPath)
	a.cli = true
	panes := &fakePanes{}
	driver := &vimProcessDriver{t: t, panes: panes}
	a.panesd, a.launcher = panes, driver
	file := filepath.Join(root, "edited ' file.txt")
	if err := os.WriteFile(file, []byte("keep this content\n"), 0600); err != nil {
		t.Fatal(err)
	}
	id := "real-vim"
	sessionPath := filepath.Join(a.vimDir, id+".vim")
	session := work.VimSession{ID: id, Name: "real", Path: sessionPath, OwnedPath: sessionPath,
		Session: mtmOnlySession, Cwd: root, Created: work.Now()}
	value, err := a.vimLaunchJob(session, Owner{}, file, false)(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	started := value.(vimStarted)
	if started.Launch.Note != "" {
		t.Fatal(started.Launch.Note)
	}
	session = started.Session
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatal(err)
	}
	owner, err := a.work.PaneOwner(session.WindowID, "")
	if err != nil || owner.AssignedAt == 0 || owner.ProjectID != "" {
		t.Fatalf("immediate scratch ownership: %+v, %v", owner, err)
	}
	vimRPCForTest(t, binary, session.Listen, "execute('vsplit')")
	mustCLI(t, a, "vim", "save", "--id", id)
	if got := vimRPCForTest(t, binary, session.Listen, "winnr('$')"); got != "2" {
		t.Fatalf("windows before save: %q", got)
	}
	// A failed :mksession is observable and does not change the prior snapshot.
	before, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeVimSession(context.Background(), binary, session.Listen, filepath.Join(root, "missing", "session.vim")); err == nil {
		t.Fatal("missing parent unexpectedly saved")
	}
	after, err := os.ReadFile(sessionPath)
	if err != nil || string(before) != string(after) {
		t.Fatalf("failed save changed snapshot: %v", err)
	}
	value, err = a.vimLaunchJob(session, Owner{}, "", true)(context.Background())
	if err != nil || len(driver.commands) != 1 || value.(vimStarted).Session.WindowID != session.WindowID {
		t.Fatalf("live reopen launched another pane: %+v, %v", value, err)
	}
	if err := driver.processes[0].Process.Kill(); err != nil {
		t.Fatal(err)
	}
	panes.panes = nil
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	b := openAt(t, dbPath)
	b.cli, b.panesd, b.launcher = true, panes, driver
	session, err = b.work.VimSession(id)
	if err != nil {
		t.Fatal(err)
	}
	value, err = b.vimLaunchJob(session, Owner{}, "", true)(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	session = value.(vimStarted).Session
	if len(driver.commands) != 2 || !slices.Contains(driver.commands[1], "-S") {
		t.Fatalf("restart argv: %v", driver.commands)
	}
	if err := waitVimRPC(context.Background(), session.Listen); err != nil {
		t.Fatal(err)
	}
	if got := vimRPCForTest(t, binary, session.Listen, "winnr('$')"); got != "2" {
		t.Fatalf("restored window count: %q", got)
	}
	restored := vimRPCForTest(t, binary, session.Listen, "expand('%:p')")
	wantInfo, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	gotInfo, err := os.Stat(restored)
	if err != nil || !os.SameFile(wantInfo, gotInfo) {
		t.Fatalf("restored buffer: %q, want file %q: %v", restored, file, err)
	}
	externalSession := filepath.Join(root, "explicit session.vim")
	mustCLI(t, b, "vim", "save", externalSession, "--id", id)
	if _, err := b.r.Execute(ochazuke.Request{Noun: "vim", Verb: "forget", Target: vimListID, Args: ochazuke.Arguments{"id": id}, Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.work.VimSession(id); err != store.ErrNotFound {
		t.Fatalf("forgotten row: %v", err)
	}
	if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
		t.Fatalf("owned session file remains: %v", err)
	}
	if _, err := os.Stat(externalSession); err != nil {
		t.Fatalf("forget deleted explicit session file: %v", err)
	}
	if text, err := os.ReadFile(file); err != nil || string(text) != "keep this content\n" {
		t.Fatalf("edited file changed: %q, %v", text, err)
	}
	if len(panes.killed) != 0 || vimRPCForTest(t, binary, session.Listen, "1+1") != "2" {
		t.Fatal("forget killed the live editor")
	}
}

func TestVimForgetLeavesExternalLegacyAndSymlinkTargets(t *testing.T) {
	root := t.TempDir()
	external := filepath.Join(root, "important.txt")
	if err := os.WriteFile(external, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, row := range []work.VimSession{
		{ID: "legacy", Path: external},
		{ID: "explicit", Path: external},
	} {
		if err := forgetVimFile(root, row); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(root, "linked.vim")
	if err := os.Symlink(external, link); err != nil {
		t.Fatal(err)
	}
	if err := forgetVimFile(root, work.VimSession{ID: "linked", OwnedPath: link}); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(external); err != nil || string(data) != "keep" {
		t.Fatalf("external data changed: %q, %v", data, err)
	}
	if err := forgetVimFile(root, work.VimSession{ID: "bad", OwnedPath: external}); err == nil {
		t.Fatal("outside owned path accepted")
	}
}

func TestVimScopeColumnsAndExplicitID(t *testing.T) {
	a := open(t)
	for _, row := range []work.VimSession{
		{ID: "vim-a", Name: "A", ProjectID: "matomeru", State: "closed"},
		{ID: "vim-b", Name: "B", ProjectID: "ochazuke", State: "closed"},
		{ID: "vim-scratch", Name: "scratch", State: "closed"},
	} {
		if err := a.work.PutVimSession(row); err != nil {
			t.Fatal(err)
		}
	}
	a.pin = "matomeru"
	if err := a.refreshVim(); err != nil {
		t.Fatal(err)
	}
	if items := a.vimList.Items(); len(items) != 1 || items[0].ID != "vim-a" || len(items[0].Cells) != 4 {
		t.Fatalf("scoped rows: %+v", items)
	}
	if len(a.vimList.Columns()) != 4 {
		t.Fatal("missing table columns")
	}
	mustCLI(t, a, "vim", "list", "--all")
	if items := a.vimList.Items(); len(items) != 1 || items[0].ID != "vim-a" {
		t.Fatalf("--all leaked into pinned list: %+v", items)
	}
	row, err := a.vimAt(ochazuke.Ctx{Args: ochazuke.Arguments{"id": "vim-b"}})
	if err != nil || row.ID != "vim-b" {
		t.Fatalf("explicit ID requires selection: %+v, %v", row, err)
	}
	for _, verb := range []string{"list", "open", "save", "reopen", "forget"} {
		if _, ok := a.r.Operations["vim."+verb]; !ok {
			t.Fatalf("missing vim.%s", verb)
		}
	}
	if _, err := a.r.ValidateBindings(); err != nil {
		t.Fatal(err)
	}
	for _, width := range []int{80, 120} {
		if a.renderVim(a.r, width, 20).Text == "" {
			t.Fatal("empty Vim rendering")
		}
	}
}

func TestVimOpenScratchWithoutProject(t *testing.T) {
	binary, err := exec.LookPath("nvim")
	if err != nil {
		t.Skip("nvim not installed; real Neovim acceptance unverified")
	}
	root := t.TempDir()
	a := openAt(t, filepath.Join(root, "mtm.db"))
	a.cli = true
	a.home = func() (string, error) { return root, nil }
	panes := &fakePanes{}
	driver := &vimProcessDriver{t: t, panes: panes}
	a.panesd, a.launcher = panes, driver
	mustCLI(t, a, "vim", "open", "--scratch")
	rows, err := a.work.VimSessions()
	if err != nil || len(rows) != 1 {
		t.Fatalf("scratch session: %+v, %v", rows, err)
	}
	row := rows[0]
	if row.ProjectID != "" || row.TaskID != "" || row.Session != mtmOnlySession {
		t.Fatalf("scratch owner: %+v", row)
	}
	if got := vimRPCForTest(t, binary, row.Listen, "bufname('%')"); got != "" {
		t.Fatalf("scratch buffer: %q", got)
	}
	if _, err := os.Stat(row.Path); err != nil {
		t.Fatalf("initial session not saved: %v", err)
	}
}

func TestVimLiveIdentitySurvivesSessionRename(t *testing.T) {
	row := work.VimSession{WindowID: "stable-pane", Session: "old-name"}
	live := pane{Session: "renamed", Pane: daemon.Pane{ID: "stable-pane"}}
	if got, ok := vimPane(row, []pane{live}); !ok || got.Session != "renamed" {
		t.Fatalf("stable pane was treated as closed after session rename: %+v, %v", got, ok)
	}
}
