package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
)

// Persistent connections to the extensions (ochazuke's host serve). A tool
// whose catalog declares the protocol (CatalogDoc.Serve, cli.Serves) is
// kept running by one cli.ServePool per mounted extension, and the calls
// mtm made by exec (a mounted verb, its preflight, the page loads, the
// parameter choices, a noun's watch) go to it instead. Exec stays the exact
// fallback: the pool execs the first calls (extensions.persistent-after), a
// call while every process is busy, and every call of a tool that does not
// serve or broke. Every rule is a setting below, read when a pool is made;
// a change makes the pools again. Nothing on screen changes, so
// extensions.transport exec, today's behaviour, is the opt-out.

const (
	settingExtTransport       = "extensions.transport"
	settingExtPersistentAfter = "extensions.persistent-after"
	settingExtPoolSize        = "extensions.pool-size"
	settingExtIdleTimeout     = "extensions.idle-timeout"
	settingExtPoolWait        = "extensions.pool-wait"
	settingExtHostCalls       = "extensions.host-calls"

	extTransportPersistent = "persistent"
	extTransportExec       = "exec"
	extHostCallsPipe       = "pipe"
	extHostCallsExec       = "exec"
)

// extPoolSettingNames are the settings a pool is made from: a change of
// any makes every pool again.
var extPoolSettingNames = []string{settingExtTransport, settingExtPersistentAfter, settingExtPoolSize, settingExtIdleTimeout, settingExtPoolWait}

func atLeastOne(v any) error {
	if n, ok := v.(int); ok && n < 1 {
		return errors.New("must be 1 or more")
	}
	return nil
}

var extPoolSettings = []ochazuke.Setting{
	{Name: settingExtTransport, Owner: extensionsOwner, Section: ochazuke.SettingsGeneralSection, Type: ochazuke.SettingString, Default: extTransportPersistent, Enum: []string{extTransportPersistent, extTransportExec},
		Description: "How mtm calls an extension whose catalog declares the host serve protocol: persistent keeps the tool running and sends it each call over a pipe (exec when busy or broken); exec starts one process per call, as before"},
	{Name: settingExtPersistentAfter, Owner: extensionsOwner, Section: ochazuke.SettingsGeneralSection, Type: ochazuke.SettingInteger, Default: 2, Validate: atLeastOne,
		Description: "The call to a tool (counted from 1 in each mtm process) from which it is kept running; the calls before it exec, so a one-shot command never pays a server's start"},
	{Name: settingExtPoolSize, Owner: extensionsOwner, Section: ochazuke.SettingsGeneralSection, Type: ochazuke.SettingInteger, Default: 2, Validate: atLeastOne,
		Description: "The most processes kept running per tool; a call while all are busy execs"},
	{Name: settingExtIdleTimeout, Owner: extensionsOwner, Section: ochazuke.SettingsGeneralSection, Type: ochazuke.SettingDuration, Default: 5 * time.Minute,
		Description: "A kept process idle this long ends (0: never); the next call starts one again"},
	{Name: settingExtPoolWait, Owner: extensionsOwner, Section: ochazuke.SettingsGeneralSection, Type: ochazuke.SettingDuration, Default: 250 * time.Millisecond,
		Description: "How long a read (a page's rows, a parameter's choices) waits for a kept process before it takes another or execs: reads share all but one of extensions.pool-size's processes, so a page's loads run one after another on a running tool instead of starting one process each; 0 never waits"},
	{Name: settingExtHostCalls, Owner: extensionsOwner, Section: ochazuke.SettingsGeneralSection, Type: ochazuke.SettingString, Default: extHostCallsPipe, Enum: []string{extHostCallsPipe, extHostCallsExec},
		Description: "How a kept tool's calls back to mtm run (Host.Call, HostCommand): pipe answers them in this process, on a headless runtime of their own; exec starts mtm for each, as before"},
}

// extPool is an extension's kept processes and the gate its reads take.
type extPool struct {
	pool *ocli.ServePool
	// reads holds a slot per read in flight; a read waits up to wait for
	// one (extensions.pool-wait), then goes to the pool anyway.
	reads chan struct{}
	wait  time.Duration
}

// newExtPool is x's pool under the settings now, nil when x execs every
// call: transport exec, a catalog that declares no serve protocol, or a
// callback runtime (whose calls exec, so a callback never waits on a pool
// whose process waits on it).
func (a *App) newExtPool(x *extension) *extPool {
	s := a.r.Settings()
	if a.callback || x.doc.Serve < 1 || s.String(settingExtTransport) == extTransportExec {
		return nil
	}
	size := max(s.Int(settingExtPoolSize), 1)
	p := &extPool{wait: s.Duration(settingExtPoolWait), reads: make(chan struct{}, max(size-1, 1))}
	p.pool = &ocli.ServePool{
		Bin:         x.bin,
		After:       max(s.Int(settingExtPersistentAfter), 1),
		Size:        size,
		IdleTimeout: s.Duration(settingExtIdleTimeout),
		WaitDelay:   extWaitDelay,
		StartEnv:    []string{"MTM_DATA", "XDG_DATA_HOME", "HOME"},
		CatalogHash: extCatalogHash(x.entry),
		// Both run off the loop: they leave a mark the loop reads
		// (extPoolEvents).
		OnStale:   func(string) { x.poolStale.Store(true) },
		Notice:    func(line string) { x.poolNotice.Store(&line) },
		OnHostRun: a.answerHostRun,
	}
	return p
}

// extCatalogHash is the hash of the catalog x was mounted from (its
// registry entry), as the tool's hello computes it of its own.
func extCatalogHash(entry []byte) string {
	var e struct {
		Catalog json.RawMessage `json:"catalog"`
	}
	if json.Unmarshal(entry, &e) != nil || len(e.Catalog) == 0 {
		return ""
	}
	return ocli.CatalogHash(e.Catalog)
}

// poolOf is the pool of x's calls now, nil to exec.
func (x *extension) poolOf() *ocli.ServePool {
	if p := x.pool.Load(); p != nil {
		return p.pool
	}
	return nil
}

// resetPool replaces x's pool with p (nil: exec) and ends the old one.
func (x *extension) resetPool(p *extPool) {
	if old := x.pool.Swap(p); old != nil {
		old.pool.Close()
	}
}

// read is one read call of the tool (its stdout, or the error execExt
// would give): served when x has a pool, else by exec. A read is retried
// once by exec when its process breaks under it.
func (p *extPool) read(ctx context.Context, bin string, env []string, args ...string) ([]byte, error) {
	if p == nil {
		return execExt(ctx, bin, env, args...)
	}
	ctx, cancel := context.WithTimeout(ctx, extExecTimeout)
	defer cancel()
	defer p.take(ctx)()
	answer, err := p.pool.Run(ctx, ocli.ServeCall{Argv: args, Env: extensionExecEnv(env, false)}, true)
	if err == nil && answer.Exit != 0 {
		err = fmt.Errorf("exit status %d", answer.Exit)
	}
	if err != nil {
		return nil, extFailed(bin, args, err, answer.Stderr)
	}
	return answer.Stdout, nil
}

// take waits for a read slot, at most p.wait; the answer gives it back.
// Without a slot in time the read goes on without one: it never blocks
// longer than an exec's start.
func (p *extPool) take(ctx context.Context) func() {
	if p.wait <= 0 {
		return func() {}
	}
	timer := time.NewTimer(p.wait)
	defer timer.Stop()
	select {
	case p.reads <- struct{}{}:
		return func() { <-p.reads }
	case <-timer.C:
	case <-ctx.Done():
	}
	return func() {}
}

// extRead is a read of x's tool through its pool now.
func (x *extension) read(ctx context.Context, env []string, args ...string) ([]byte, error) {
	return x.pool.Load().read(ctx, x.bin, env, args...)
}

// extWatch is a noun's watch: on a kept process when x has a pool
// (cli.PoolWatch), else its own process (execExtWatch).
func (a *App) extWatch(x *extension, noun string, q ochazuke.WatchQuery, emit func(ochazuke.WatchEvent)) (func(), error) {
	env := a.extEnv(x.name)
	keyProp := extensionKeyProp(x.doc, noun)
	pool := x.poolOf()
	if pool == nil {
		return execExtWatch(x.bin, env, keyProp, noun, q, emit)
	}
	words, err := extWatchWords(keyProp, noun, q)
	if err != nil {
		return nil, err
	}
	call := ocli.ServeCall{Argv: words, Env: extensionExecEnv(env, false)}
	return ocli.PoolWatch(pool, func(string, ochazuke.WatchQuery) ocli.ServeCall { return call })(noun, q, emit)
}

// extPoolEvents shows on the loop what x's pool said off it: a notice
// that it went exec-only (the status line of x's pages), and a changed
// binary or catalog (the catalog check, which refreshes it when it
// differs).
func (a *App) extPoolEvents(x *extension) {
	if line := x.poolNotice.Swap(nil); line != nil {
		for _, v := range x.views {
			v.SetStatus(*line)
		}
	}
	if x.poolStale.Swap(false) {
		a.checkStaleExtensions(x.name)
	}
}

// observeExtPools makes every pool again when a pool setting changes from
// the value the pools were made with, and keeps the callback mode.
func (a *App) observeExtPools() {
	s := a.r.Settings()
	if cb := a.callbacks; cb != nil {
		cb.exec.Store(s.String(settingExtHostCalls) == extHostCallsExec)
		s.Observe(settingExtHostCalls, func(_ string, v any) { cb.exec.Store(v == extHostCallsExec) })
	}
	for _, name := range extPoolSettingNames {
		made := fmt.Sprint(s.Value(name))
		s.Observe(name, func(_ string, v any) {
			if now := fmt.Sprint(v); now != made {
				made = now
				for _, x := range a.exts {
					x.resetPool(a.newExtPool(x))
				}
			}
		})
	}
}

// closeExtPools ends every kept process (mtm is quitting) and the
// callback runtime.
func (a *App) closeExtPools() error {
	for _, x := range a.exts {
		x.resetPool(nil)
	}
	cb := a.callbacks
	if cb == nil {
		return nil
	}
	// A callback running now ends first (its turn), so its runtime is
	// closed idle; one that does not end soon is left to the exit.
	select {
	case cb.turn <- struct{}{}:
	case <-time.After(extExecTimeout / 30):
		return nil
	}
	defer func() { <-cb.turn }()
	if cb.app != nil {
		app := cb.app
		cb.app, cb.err = nil, errors.New("mtm is closing")
		return app.Close()
	}
	return nil
}

// The extension-connection noun: one row per kept process.

const extConnectionNoun = "extension-connection"

var extConnectionColumns = []ochazuke.Column{
	{Title: "extension"},
	{Title: "pid", Align: ochazuke.AlignRight},
	{Title: "state"},
	{Title: "calls", Align: ochazuke.AlignRight},
	{Title: "started", Priority: 1},
	{Title: "last error", Priority: 2},
}

// extConnection is one kept process of an extension.
type extConnection struct {
	ID        string    `json:"id"`
	Extension string    `json:"extension"`
	PID       int       `json:"pid"`
	State     string    `json:"state"`
	Calls     int       `json:"calls"`
	Started   time.Time `json:"started"`
	Stamp     string    `json:"stamp"`
	LastError string    `json:"lastError,omitempty"`
}

func (a *App) defineExtConnections() {
	n := extConnectionNoun
	a.r.Define(n).Columns(extConnectionColumns...).
		Verb("all", a.extConnections, "List the extensions' kept processes (extensions.transport persistent): pid, state, calls served, when it started").Rows(n).Fetch().
		Verb("show", a.showExtConnection, "Show one kept process of an extension").Positional("id").Fetch().
		Verb("restart", a.restartExtConnections, "Restart an extension's kept processes: idle ones end now, busy ones after their call; the next call starts a fresh one (and serving is tried again after a failure)").Positional("id").
		Verb("yank", a.yankExtConnection, "Copy a kept process's pid to the clipboard").Positional("id")
	a.r.Define("extension").
		Verb("reconnect", a.restartExtConnections, "Restart an extension's kept processes (extension-connection restart for all of them)").Positional("name")
	id := ochazuke.Parameter{Type: "string", Noun: n, Description: "The connection (EXTENSION/PID); empty: the selected row"}
	name := ochazuke.Parameter{Type: "string", Description: "Only this extension's connections"}
	a.parameters(n+".all", map[string]ochazuke.Parameter{"name": name})
	a.parameters(n+".show", map[string]ochazuke.Parameter{"id": id})
	a.parameters(n+".restart", map[string]ochazuke.Parameter{"id": id, "name": {Type: "string", Description: "Every connection of this extension"}})
	a.parameters(n+".yank", map[string]ochazuke.Parameter{"id": id})
	a.parameters("extension.reconnect", map[string]ochazuke.Parameter{"name": {Type: "string", Description: "The extension; omitted, every one"}})
}

// connections are the kept processes of every extension (or of name).
func (a *App) connections(name string) []extConnection {
	out := []extConnection{}
	for _, x := range a.exts {
		pool := x.poolOf()
		if pool == nil || (name != "" && x.name != name) {
			continue
		}
		for _, c := range pool.Connections() {
			out = append(out, extConnection{ID: x.name + "/" + strconv.Itoa(c.PID), Extension: x.name, PID: c.PID, State: c.State, Calls: c.Calls, Started: c.Started, Stamp: c.Stamp, LastError: c.LastError})
		}
	}
	return out
}

func extConnectionRow(c extConnection) ocli.Row {
	return ocli.Row{Noun: extConnectionNoun, ID: c.ID, Label: c.ID, Data: c,
		Cells: []string{c.Extension, strconv.Itoa(c.PID), c.State, strconv.Itoa(c.Calls), c.Started.Format(time.TimeOnly), c.LastError}}
}

// extConnections is extension-connection all.
func (a *App) extConnections(c ochazuke.Ctx) ochazuke.Result {
	rows := []ocli.Row{}
	for _, conn := range a.connections(ochazuke.StringArg(c.Args, "name", "")) {
		rows = append(rows, extConnectionRow(conn))
	}
	return ochazuke.Done(rows)
}

// connectionID is the id argument, else the selected connection row's.
func connectionID(c ochazuke.Ctx) string {
	id := ochazuke.StringArg(c.Args, "id", "")
	if id == "" && c.Instance != nil {
		if conn, ok := c.Instance.Data.(extConnection); ok {
			id = conn.ID
		}
	}
	return id
}

func (a *App) findConnection(c ochazuke.Ctx) (extConnection, error) {
	id := connectionID(c)
	if id == "" {
		return extConnection{}, errors.New("name a connection (extension-connection show EXTENSION/PID) or select its row")
	}
	for _, conn := range a.connections("") {
		if conn.ID == id {
			return conn, nil
		}
	}
	return extConnection{}, fmt.Errorf("no connection %s (extension-connection all lists them)", id)
}

func (a *App) showExtConnection(c ochazuke.Ctx) ochazuke.Result {
	conn, err := a.findConnection(c)
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(conn)
}

func (a *App) yankExtConnection(c ochazuke.Ctx) ochazuke.Result {
	conn, err := a.findConnection(c)
	if err != nil {
		return ochazuke.Failed(err)
	}
	pid := strconv.Itoa(conn.PID)
	if err := a.copyOSC52(pid); err != nil {
		return ochazuke.Failed(err)
	}
	c.Notify("Yanked " + pid)
	return ochazuke.Done(pid)
}

// restartExtConnections is extension-connection restart and
// extension.reconnect: the pool of the named connection's extension (or
// of name, or every pool) drains. The pool restarts as a whole: its
// processes are interchangeable.
func (a *App) restartExtConnections(c ochazuke.Ctx) ochazuke.Result {
	name := ochazuke.StringArg(c.Args, "name", "")
	if id := connectionID(c); id != "" {
		conn, err := a.findConnection(c)
		if err != nil {
			return ochazuke.Failed(err)
		}
		name = conn.Extension
	}
	restarted := []string{}
	for _, x := range a.exts {
		if pool := x.poolOf(); pool != nil && (name == "" || x.name == name) {
			pool.Restart()
			restarted = append(restarted, x.name)
		}
	}
	if name != "" && !slices.Contains(restarted, name) {
		return ochazuke.Failed(fmt.Errorf("%s keeps no processes (its catalog declares no serve protocol, or extensions.transport is exec)", name))
	}
	return ochazuke.Done(map[string]any{"restarted": restarted})
}
