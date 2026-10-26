package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/harness"
	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
	ocli "github.com/unixsuperhero/ochazuke/cli"
)

const vimPage = "vim-sessions"
const vimListID = "vim-sessions/list"

var vimColumns = []ochazuke.Column{
	{Title: "name", Min: 16}, {Title: "project", Priority: 3},
	{Title: "state", Priority: 1}, {Title: "session file", Priority: 2},
}

func (a *App) defineVim() {
	a.r.Define("vim").Columns(vimColumns...).
		Verb("list", a.listVim, "List Neovim sessions in scope; --all widens it").Rows("vim").
		Verb("open", a.openVim, "Open real Neovim; optional file or directory, otherwise a scratch buffer").Positional("path").
		Verb("save", a.saveVim, "Write :mksession through Neovim RPC; edited files are not written").Positional("path").
		Verb("reopen", a.reopenVim, "Attach the live pane or restore its saved Neovim session").Positional("id").
		Verb("forget", a.forgetVim, "Forget metadata and mtm-owned session file; keep edited files and live pane").Positional("id").Confirm("Forget {{label}}? Edited files and the live pane stay.").
		Mode("normal").Bind("n", ochazuke.Do("vim.open")).Bind("w", ochazuke.Do("vim.save")).
		Bind("enter", ochazuke.Do("vim.reopen")).Bind("d d", ochazuke.Do("vim.forget"))
	a.parameters("vim.list", map[string]ochazuke.Parameter{"all": {Type: "boolean"}})
	a.parameters("vim.open", map[string]ochazuke.Parameter{
		"path": {Type: "string"}, "name": {Type: "string"}, "project": {Type: "string"},
		"task": {Type: "string"}, "scratch": {Type: "boolean"},
	})
	a.parameters("vim.save", map[string]ochazuke.Parameter{"id": {Type: "string"}, "pane": {Type: "string"}, "path": {Type: "string"}})
	for _, verb := range []string{"reopen", "forget"} {
		a.parameters("vim."+verb, map[string]ochazuke.Parameter{"id": {Type: "string"}})
	}
	a.ownedBy("vim", vimListID)
}

func (a *App) addVimPage() error {
	a.r.Instance(vimPage, ochazuke.Kind("page"))
	var err error
	a.vimList, err = a.r.AddList(vimListID, nil, ochazuke.Parent(vimPage), ochazuke.Columns(vimColumns...))
	if err != nil {
		return err
	}
	if _, err := a.r.AddPage(vimPage, "Vim", a.renderVim, ochazuke.Focus(vimListID)); err != nil {
		return err
	}
	// n opens one from the empty list too; the list refreshes itself
	// (on opening the page and with every pane listing), so no key does.
	row := ochazuke.Do("vim.open")
	row.Instance, row.Mode, row.Keys = vimListID, "normal", "n"
	if err := a.r.Bind(row); err != nil {
		return err
	}
	return a.refreshVim()
}

func (a *App) vimRows() ([]ocli.Row, error) { return a.vimRowsWith(a.vimState) }

// vimRowsWith is the rows in scope, each session's state from state.
func (a *App) vimRowsWith(state func(work.VimSession) string) ([]ocli.Row, error) {
	sessions, err := a.work.VimSessions()
	if err != nil {
		return nil, err
	}
	a.vimAll = sessions
	rows := make([]ocli.Row, 0, len(sessions))
	home, _ := a.home() // paths read with ~, as on every other page
	for _, session := range sessions {
		project, err := a.work.VimProject(session)
		if err != nil {
			return nil, err
		}
		if !a.inScope(project) {
			continue
		}
		label := session.Name
		if label == "" {
			label = "untitled"
		}
		session.State = state(session)
		rows = append(rows, ocli.Row{Noun: "vim", ID: session.ID, Label: label,
			Cells: []string{label, a.ownerLabel(project), session.State, tildePath(session.Path, home)}, Data: session})
	}
	return rows, nil
}

// vimState is a session's state from the last pane listing: running
// while its window is listed and its Neovim has not exited, else closed.
// Before any listing it is the stored state.
func (a *App) vimState(session work.VimSession) string {
	if !a.pv.listed {
		return session.State
	}
	if session.WindowID != "" {
		p, ok := a.companionPanes[session.WindowID]
		if !ok {
			for _, q := range a.panes {
				if q.ID == session.WindowID {
					p, ok = q, true
					break
				}
			}
		}
		if ok && !p.Exited {
			return "running"
		}
	}
	return "closed"
}

func (a *App) refreshVim() error {
	if a.vimList == nil {
		return nil
	}
	rows, err := a.vimRows()
	if err != nil {
		return err
	}
	items := make([]ochazuke.ListItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, ochazuke.ListItem{ID: row.ID, Noun: row.Noun, Label: row.Label, Cells: row.Cells, Data: row.Data})
	}
	return a.vimList.SetItems(items)
}

func (a *App) listVim(ctx ochazuke.Ctx) ochazuke.Result {
	if a.cli {
		// The CLI has no pane listing: read the daemon off the loop, so
		// the state column says which windows still run.
		// A daemon that does not answer leaves the stored states.
		d, all := a.panesd, a.all
		if v, _ := ctx.Args["all"].(bool); v {
			all = true
		}
		return ochazuke.Pending(func(c context.Context) (any, error) {
			state := func(s work.VimSession) string { return s.State }
			if panes, err := d.ListPanes(c); err == nil {
				exited, _ := probeExits(c, d, panes)
				running := map[string]bool{}
				for _, p := range panes {
					running[p.ID] = !exited[p.ID]
				}
				state = func(s work.VimSession) string {
					if running[s.WindowID] {
						return "running"
					}
					return "closed"
				}
			}
			previous := a.all
			a.all = all
			defer func() { a.all = previous }()
			return a.vimRowsWith(state)
		})
	}
	// r also asks the daemon again; the rows follow its listing.
	if err := a.reloadPanes(); err != nil {
		a.m.Status = err.Error()
	}
	if err := a.refreshVim(); err != nil {
		return ochazuke.Failed(err)
	}
	previous := a.all
	if all, _ := ctx.Args["all"].(bool); all {
		a.all = true
	}
	defer func() { a.all = previous }()
	rows, err := a.vimRows()
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(rows)
}

func (a *App) renderVim(_ *ochazuke.Runtime, width, height int) ochazuke.Block {
	return ochazuke.Text(a.view(a.vimList, width, height))
}

func (a *App) vimAt(ctx ochazuke.Ctx) (work.VimSession, error) {
	if id := ochazuke.StringArg(ctx.Args, "id", ""); id != "" {
		return a.work.VimSession(id)
	}
	window := ochazuke.StringArg(ctx.Args, "pane", "")
	if window == "" && ctx.Instance != nil {
		if ctx.Instance.Noun == "vim" {
			return a.work.VimSession(ctx.Instance.ID)
		}
		if ctx.Instance.Noun == "pane" {
			p, err := a.paneAt(ctx.Instance)
			if err != nil {
				return work.VimSession{}, err
			}
			window = p.ID
		}
	}
	if window != "" {
		rows, err := a.work.VimSessions()
		if err != nil {
			return work.VimSession{}, err
		}
		for _, row := range rows {
			if row.WindowID == window {
				return row, nil
			}
		}
	}
	return work.VimSession{}, errors.New("select a Vim session or supply --id (save also accepts --pane)")
}

func (a *App) openVim(ctx ochazuke.Ctx) ochazuke.Result {
	owner, err := a.resolveOwner(ochazuke.Ctx{}, ctx.Args)
	if err != nil {
		return ochazuke.Failed(err)
	}
	project, _ := a.project(owner.ProjectID)
	cwd := a.startCwd(project)
	path := ochazuke.StringArg(ctx.Args, "path", "")
	if path != "" {
		path, err = filepath.Abs(path)
		if err != nil {
			return ochazuke.Failed(err)
		}
		info, statErr := os.Stat(path)
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return ochazuke.Failed(statErr)
		}
		if statErr == nil && info.IsDir() {
			cwd, path = path, ""
		} else {
			cwd = filepath.Dir(path)
		}
	}
	dir, err := filepath.Abs(a.vimDir)
	if err != nil {
		return ochazuke.Failed(err)
	}
	id, now := work.NewID(), work.Now()
	session := work.VimSession{ID: id, Name: ochazuke.StringArg(ctx.Args, "name", "vim"),
		ProjectID: owner.ProjectID, TaskID: owner.TaskID, Cwd: cwd, Created: now, Updated: now,
		Session: mtmOnlySession, State: "closed", Path: filepath.Join(dir, id+".vim")}
	if project.ID != "" {
		session.Session = sessionFor(project)
	}
	session.OwnedPath = session.Path
	return a.startVim(session, owner, path, false)
}

func (a *App) reopenVim(ctx ochazuke.Ctx) ochazuke.Result {
	session, err := a.vimAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	project, err := a.work.VimProject(session)
	if err != nil {
		return ochazuke.Failed(err)
	}
	return a.startVim(session, Owner{ProjectID: project, TaskID: session.TaskID}, "", true)
}

type vimStarted struct {
	Session work.VimSession `json:"session"`
	Launch  launched        `json:"launch"`
}

func vimPane(session work.VimSession, panes []pane) (pane, bool) {
	for _, p := range panes {
		if session.WindowID != "" && p.ID == session.WindowID {
			return p, true
		}
	}
	return pane{}, false
}

func (a *App) startVim(session work.VimSession, owner Owner, path string, reopen bool) ochazuke.Result {
	version := a.pinVersion
	return ochazuke.Pending(a.vimLaunchJob(session, owner, path, reopen)).Then(func(_ ochazuke.Ctx, value any, err error) {
		if done, ok := value.(vimStarted); ok && err == nil {
			if refreshErr := a.refreshVim(); refreshErr != nil {
				a.m.Notify(refreshErr.Error())
			}
			if !a.cli {
				a.finishLaunch(done.Launch, owner, version)
			}
		}
	})
}

func (a *App) vimLaunchJob(session work.VimSession, owner Owner, path string, reopen bool) func(context.Context) (any, error) {
	driver := &recordingDriver{Driver: a.launcher, work: a.work, owner: owner}
	panesd, workStore, dir := a.panesd, a.work, a.vimDir
	destination := mtmOnlySession
	if project, ok := a.project(owner.ProjectID); ok {
		destination = sessionFor(project)
	}
	return launchVimJob(session, path, reopen, driver, panesd, workStore, dir, destination)
}

func launchVimJob(session work.VimSession, path string, reopen bool, driver *recordingDriver, panesd paneDaemon, workStore *work.Store, dir, destination string) func(context.Context) (any, error) {
	return func(ctx context.Context) (any, error) {
		panes, err := panesd.ListPanes(ctx)
		if err != nil && reopen {
			return nil, fmt.Errorf("check live Vim pane before reopening: %w", err)
		}
		if p, live := vimPane(session, panes); reopen && live {
			if err := workStore.TouchDir(p.Cwd); err != nil {
				driver.note(err)
			}
			session.State = "running"
			return vimStarted{Session: session, Launch: launched{Pane: harness.Result{Session: p.Session, Window: p.ID, Name: p.Name, Prompt: harness.PromptNone}, Panes: panes, Note: strings.Join(driver.notes, "; ")}}, nil
		}
		if reopen {
			session.Session = destination
		}
		binary, err := exec.LookPath("nvim")
		if err != nil {
			return nil, errors.New("Neovim executable nvim is not on PATH; install Neovim to open a session")
		}
		if reopen {
			info, err := os.Stat(session.Path)
			if err != nil {
				return nil, fmt.Errorf("saved Vim session %s: %w", session.Path, err)
			}
			if !info.Mode().IsRegular() {
				return nil, errors.New("saved Vim session is not a regular file")
			}
		}
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
		// Short private socket paths also fit Darwin's Unix socket limit when
		// the database lives in a deeply nested directory.
		socketDir, err := os.MkdirTemp("", "mtm-vim-")
		if err != nil {
			return nil, err
		}
		session.Listen = filepath.Join(socketDir, "nvim.sock")
		command := []string{binary, "--listen", session.Listen}
		if reopen {
			command = append(command, "-S", session.Path)
		}
		if path != "" {
			command = append(command, "--", path)
		}
		session.State, session.Updated = "closed", work.Now()
		if err := workStore.PutVimSession(session); err != nil {
			os.RemoveAll(socketDir)
			return nil, err
		}
		id, err := driver.OpenWindow(ctx, harness.Window{Session: session.Session, Name: session.Name, Cwd: session.Cwd, Command: command})
		if err != nil {
			os.RemoveAll(socketDir)
			return nil, err
		}
		if id == "" {
			return nil, errors.New("Neovim launch returned no pane id; inspect the daemon before retrying")
		}
		session.WindowID, session.State, session.Updated = id, "running", work.Now()
		if err := workStore.PutVimSession(session); err != nil {
			driver.note(fmt.Errorf("Vim pane %s exists but its session link was not saved: %w", id, err))
		}
		// Only initial creation writes automatically; reopening must not replace
		// the saved state before restoration has finished.
		if !reopen {
			if err := waitVimRPC(ctx, session.Listen); err != nil {
				driver.note(err)
			} else if err := writeVimSession(ctx, binary, session.Listen, session.Path); err != nil {
				driver.note(err)
			}
		}
		rows, err := panesd.ListPanes(ctx)
		if err != nil {
			driver.note(fmt.Errorf("Vim pane %s is open; listing panes failed: %w", id, err))
		}
		if _, ok := vimPane(session, rows); !ok {
			rows = append(rows, pane{Session: session.Session, Pane: daemon.Pane{ID: id, Name: session.Name, Cwd: session.Cwd}})
		}
		if driver.owner.Notice != "" {
			driver.notes = append(driver.notes, driver.owner.Notice)
		}
		return vimStarted{Session: session, Launch: launched{Pane: harness.Result{Session: session.Session, Window: id, Name: session.Name, Argv: command, Prompt: harness.PromptNone}, Panes: rows, Note: strings.Join(driver.notes, "; ")}}, nil
	}
}

func waitVimRPC(ctx context.Context, socket string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
		if err == nil {
			conn.Close()
			return nil
		}
		if err := harness.Sleep(ctx, 25*time.Millisecond); err != nil {
			return fmt.Errorf("Neovim RPC did not become ready: %w", err)
		}
	}
}

// Vim single-quoted literals double apostrophes; no path enters Ex syntax.
func vimLiteral(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }

func writeVimSession(ctx context.Context, binary, socket, path string) error {
	if socket == "" {
		return errors.New("legacy Vim pane has no RPC address; close it and reopen its saved session to enable saving")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	lua := `(function() local ok, err = pcall(vim.cmd, 'mksession! ' .. vim.fn.fnameescape(_A)); return {ok=ok, error=ok and '' or tostring(err)} end)()`
	expr := "json_encode(luaeval(" + vimLiteral(lua) + ", " + vimLiteral(path) + "))"
	output, err := exec.CommandContext(ctx, binary, "--server", socket, "--remote-expr", expr).CombinedOutput()
	if err != nil {
		return fmt.Errorf("Neovim session save: %w: %s", err, strings.TrimSpace(string(output)))
	}
	var result struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return fmt.Errorf("Neovim save response: %w: %s", err, output)
	}
	if !result.OK {
		return fmt.Errorf("Neovim :mksession: %s", result.Error)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("Neovim reported save success but session file is unavailable: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("Neovim session output is not a regular file")
	}
	return nil
}

func (a *App) saveVim(ctx ochazuke.Ctx) ochazuke.Result {
	session, err := a.vimAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	path := ochazuke.StringArg(ctx.Args, "path", session.Path)
	path, err = filepath.Abs(path)
	if err != nil {
		return ochazuke.Failed(err)
	}
	workStore, panesd := a.work, a.panesd
	return ochazuke.Pending(func(c context.Context) (any, error) {
		panes, err := panesd.ListPanes(c)
		if err != nil {
			return nil, err
		}
		if _, live := vimPane(session, panes); !live {
			return nil, errors.New("Vim pane is closed; reopen before saving")
		}
		binary, err := exec.LookPath("nvim")
		if err != nil {
			return nil, err
		}
		if err := writeVimSession(c, binary, session.Listen, path); err != nil {
			return nil, err
		}
		// Read again so a concurrent ownership edit/deletion is not reverted.
		current, err := workStore.VimSession(session.ID)
		if err != nil {
			return nil, err
		}
		current.Path, current.Updated = path, work.Now()
		if err := workStore.PutVimSession(current); err != nil {
			return nil, err
		}
		return current, nil
	}).Then(func(_ ochazuke.Ctx, _ any, err error) {
		if err == nil {
			if err := a.refreshVim(); err != nil {
				a.m.Notify(err.Error())
			}
		}
	})
}

func (a *App) forgetVim(ctx ochazuke.Ctx) ochazuke.Result {
	session, err := a.vimAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if err := forgetVimFile(a.vimDir, session); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.work.ForgetVimSession(session.ID); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.refreshVim(); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(session)
}

func forgetVimFile(dir string, session work.VimSession) error {
	if session.OwnedPath == "" {
		return nil
	}
	// Never trust an arbitrary persisted path or follow a session-file symlink.
	if session.ID == "" || filepath.Base(session.ID) != session.ID {
		return errors.New("invalid Vim session id")
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	owned := filepath.Join(root, session.ID+".vim")
	if session.OwnedPath != owned {
		return errors.New("refusing to remove Vim session file outside mtm storage")
	}
	err = os.Remove(owned)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
