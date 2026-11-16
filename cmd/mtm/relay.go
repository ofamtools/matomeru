package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/store"
)

// The UI relay: a verb whose state is the running TUI's memory (ochazuke's
// restart request) does nothing useful in an agent's own mtm process, a
// command or the MCP gateway, where a fresh headless runtime holds its own
// copy of that state. uiProcessVerbs names such verbs (data on the verb);
// one wrapper (defineUIRelay) makes each of them, outside a TUI, run in
// every TUI open on the same database instead.
//
// The channel is the one other processes already reach a running TUI by:
// the shared SQLite file, read on a timer through the store's cheap change
// token (as the Inbox reads the action items an agent adds). A TUI records
// itself in uiHostsTable while it runs; a relayed request is a row of
// uiCallsTable; each TUI runs it on its loop as the caller's actor (the
// rights gate judges the agent there too) and answers in uiAnswersTable;
// the caller waits for every live TUI's answer and returns the first.
// With no TUI running, the verb answers nothing with the reason.

// uiProcessVerbs are the verbs whose state lives in the TUI process.
var uiProcessVerbs = []string{
	"ochazuke.restart-request.post",
	"ochazuke.restart-request.show",
	"ochazuke.restart-request.dismiss",
}

var (
	uiHostsTable   = store.Table{Name: "ui_hosts", Owner: "relay", Key: "id", Version: 1}
	uiCallsTable   = store.Table{Name: "ui_calls", Owner: "relay", Key: "id", Version: 1}
	uiAnswersTable = store.Table{Name: "ui_answers", Owner: "relay", Key: "id", Version: 1}
)

const (
	uiRelayEvery = time.Second             // how often a TUI looks for requests
	uiRelayWait  = 3 * time.Second         // how long a caller waits for the TUIs
	uiRelayPoll  = 50 * time.Millisecond   // how often the caller looks for answers
	uiRelayKeep  = 10 * time.Minute        // a request no TUI answered is dropped after
	uiRelaySrc   = "relay"                 // the history source of a relayed run
	uiHostPrefix = "pid "                  // a host row's label: pid N
	uiRelayNone  = "no mtm is running on " // the nothing reason's start
)

// uiHost is a running TUI.
type uiHost struct {
	ID      string `json:"id"`
	PID     int    `json:"pid"`
	Started int64  `json:"started"` // unix nanoseconds
	Version string `json:"version"`
}

// uiCall is a relayed request.
type uiCall struct {
	ID      string           `json:"id"`
	Request ochazuke.Request `json:"request"`
	Actor   ochazuke.Actor   `json:"actor"`
	At      int64            `json:"at"`
}

// uiAnswer is one TUI's answer to a call.
type uiAnswer struct {
	ID     string          `json:"id"`
	Call   string          `json:"call"`
	Host   string          `json:"host"`
	Result json.RawMessage `json:"result,omitempty"`
	// Outcome is how the run ended when it did not simply answer (nothing,
	// needs, failed), as the TUI's runtime classified it; Error its text.
	Outcome *ochazuke.Outcome `json:"outcome,omitempty"`
	Error   string            `json:"error,omitempty"`
	At      int64             `json:"at"`
}

type uiRelayDue struct{}

// uiRelay is a process's side of the relay. host is set only in a TUI.
type uiRelay struct {
	host  *uiHost
	tok   string
	every time.Duration
	wait  time.Duration
	poll  time.Duration
	now   func() time.Time
	alive func(pid int) bool
	after func(time.Duration, tea.Msg) tea.Cmd
}

func newUIRelay() *uiRelay {
	return &uiRelay{every: uiRelayEvery, wait: uiRelayWait, poll: uiRelayPoll, now: time.Now, alive: pidAlive,
		after: func(d time.Duration, msg tea.Msg) tea.Cmd {
			return tea.Tick(d, func(time.Time) tea.Msg { return msg })
		}}
}

// pidAlive reports whether a process with pid exists (EPERM: it does, as
// another user's).
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// defineUIRelay declares the relay's tables and wraps every UI-process
// verb. Where the tables cannot be declared the verbs stay as they are
// (they act on this process's runtime) and the failure is said.
func (a *App) defineUIRelay() {
	for _, t := range []store.Table{uiHostsTable, uiCallsTable, uiAnswersTable} {
		if err := a.db.Declare(t); err != nil {
			a.r.UI().Error("relay: " + err.Error())
			return
		}
	}
	if a.relay == nil {
		a.relay = newUIRelay()
	}
	for _, key := range uiProcessVerbs {
		op, ok := a.r.Operation(key)
		if !ok || op.Run == nil {
			continue
		}
		local, noun, verb := op.Run, op.Noun(), op.Name()
		// The caller's process needs no daemon for it (daemon_runner.go).
		if a.localVerbs == nil {
			a.localVerbs = map[string]bool{}
		}
		a.localVerbs[noun+"."+verb] = true
		op.Run = func(r *ochazuke.Runtime, c ochazuke.Context, args ochazuke.Arguments) (any, error) {
			if a.relay.host != nil {
				return local(r, c, args)
			}
			return a.relayToUI(r, ochazuke.Request{Noun: noun, Verb: verb, Args: args})
		}
		a.r.Operations.Set(op)
	}
}

// relayToUI runs q in every TUI open on this database and answers the
// first one's result (the TUIs sorted by start).
func (a *App) relayToUI(r *ochazuke.Runtime, q ochazuke.Request) (any, error) {
	hosts, err := a.uiHosts()
	if err != nil {
		return nil, err
	}
	if len(hosts) == 0 {
		return nil, ochazuke.NothingErr(ochazuke.CauseNotHere, uiRelayNone+a.dataPath+", and this verb acts on a running mtm: nothing ran")
	}
	rl := a.relay
	call := uiCall{ID: relayID(), Request: q, Actor: r.Actor(), At: rl.now().UnixNano()}
	if err := a.db.Tx([]string{uiCallsTable.Name}, func(tx *store.Tx) error { return tx.Put(uiCallsTable.Name, call.ID, call) }); err != nil {
		return nil, err
	}
	answers := map[string]uiAnswer{}
	deadline := rl.now().Add(rl.wait)
	for {
		for _, h := range hosts {
			if _, ok := answers[h.ID]; ok {
				continue
			}
			var ans uiAnswer
			if err := a.db.Get(uiAnswersTable.Name, relayAnswerID(call.ID, h.ID), &ans); err == nil {
				answers[h.ID] = ans
			}
		}
		if len(answers) == len(hosts) || !rl.now().Before(deadline) {
			break
		}
		time.Sleep(rl.poll)
	}
	names := func(want bool) []string {
		var out []string
		for _, h := range hosts {
			if _, ok := answers[h.ID]; ok == want {
				out = append(out, uiHostPrefix+strconv.Itoa(h.PID))
			}
		}
		return out
	}
	if len(answers) == len(hosts) {
		// Every TUI answered: the request and its answers are spent. A
		// TUI that has not answered yet still finds the request (it is
		// dropped after uiRelayKeep).
		_ = a.db.Tx([]string{uiCallsTable.Name, uiAnswersTable.Name}, func(tx *store.Tx) error {
			for _, h := range hosts {
				if err := tx.Delete(uiAnswersTable.Name, relayAnswerID(call.ID, h.ID)); err != nil {
					return err
				}
			}
			return tx.Delete(uiCallsTable.Name, call.ID)
		})
	}
	if len(answers) == 0 {
		return nil, ochazuke.NothingErr(ochazuke.CauseNotHere, fmt.Sprintf("the running mtm (%s) did not answer within %s; it runs the request when it next looks", strings.Join(names(false), ", "), rl.wait))
	}
	r.UI().Info("ran in the running mtm: " + strings.Join(names(true), ", "))
	for _, h := range hosts {
		ans, ok := answers[h.ID]
		if !ok {
			continue
		}
		var result any
		if len(ans.Result) > 0 {
			result = ans.Result
		}
		if ans.Outcome != nil {
			// The TUI's outcome, read back as the CLI reads a socket's.
			text := ans.Error
			if text == "" {
				text = ans.Outcome.Text()
			}
			return result, &ochazuke.OutcomeError{Outcome: *ans.Outcome, Text: text}
		}
		return result, nil
	}
	return nil, nil
}

// uiHosts are the TUIs running on this database, by start; rows whose
// process is gone are dropped.
func (a *App) uiHosts() ([]uiHost, error) {
	rows, err := a.db.All(uiHostsTable.Name)
	if err != nil {
		return nil, err
	}
	var live []uiHost
	var gone []string
	for _, raw := range rows {
		var h uiHost
		if json.Unmarshal(raw, &h) != nil || h.ID == "" {
			continue
		}
		if a.relay.alive(h.PID) {
			live = append(live, h)
		} else {
			gone = append(gone, h.ID)
		}
	}
	if len(gone) > 0 {
		_ = a.db.Tx([]string{uiHostsTable.Name}, func(tx *store.Tx) error {
			for _, id := range gone {
				if err := tx.Delete(uiHostsTable.Name, id); err != nil {
					return err
				}
			}
			return nil
		})
	}
	slices.SortFunc(live, func(x, y uiHost) int { return int(x.Started - y.Started) })
	return live, nil
}

// hostUIRelay records this TUI as a relay host. A failure is said and the
// TUI runs on: relayed requests then answer that no mtm is running.
func (a *App) hostUIRelay() {
	if a.relay == nil {
		a.relay = newUIRelay()
	}
	now := a.relay.now()
	h := &uiHost{ID: strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(now.UnixNano(), 36), PID: os.Getpid(), Started: now.UnixNano(), Version: versionLine(buildStamp())}
	if _, err := a.uiHosts(); err != nil { // drops the rows of TUIs that are gone
		a.r.UI().Error("relay: " + err.Error())
	}
	if err := a.db.Tx([]string{uiHostsTable.Name}, func(tx *store.Tx) error { return tx.Put(uiHostsTable.Name, h.ID, h) }); err != nil {
		a.r.UI().Error("relay: " + err.Error())
		return
	}
	a.relay.host = h
}

// leaveUIRelay drops this TUI's host row (Close).
func (a *App) leaveUIRelay() {
	if a.relay == nil || a.relay.host == nil {
		return
	}
	id := a.relay.host.ID
	_ = a.db.Tx([]string{uiHostsTable.Name}, func(tx *store.Tx) error { return tx.Delete(uiHostsTable.Name, id) })
}

// uiRelayTick schedules the next look for requests; nil outside a TUI.
func (a *App) uiRelayTick() tea.Cmd {
	if a.relay == nil || a.relay.host == nil || a.relay.after == nil {
		return nil
	}
	return a.relay.after(a.relay.every, uiRelayDue{})
}

// uiRelayUpdate runs the requests on the timer.
func (a *App) uiRelayUpdate(msg tea.Msg) tea.Cmd {
	if _, ok := msg.(uiRelayDue); !ok {
		return nil
	}
	if err := a.serveUIRelay(); err != nil {
		a.r.UI().Error("relay: " + err.Error())
	}
	return a.uiRelayTick()
}

// serveUIRelay runs, on the loop, each request posted since this TUI
// started that it has not answered, and drops requests older than
// uiRelayKeep. It reads the rows only when the calls' token moved.
func (a *App) serveUIRelay() error {
	rl := a.relay
	if rl == nil || rl.host == nil {
		return nil
	}
	tok, err := a.db.Version(uiCallsTable.Name)
	if err != nil {
		return err
	}
	if tok == rl.tok {
		return nil
	}
	rows, err := a.db.All(uiCallsTable.Name)
	if err != nil {
		return err
	}
	now := rl.now()
	var stale []string
	for _, raw := range rows {
		var call uiCall
		if json.Unmarshal(raw, &call) != nil || call.ID == "" {
			continue
		}
		if now.Sub(time.Unix(0, call.At)) > uiRelayKeep {
			stale = append(stale, call.ID)
			continue
		}
		if call.At < rl.host.Started {
			continue // posted before this TUI started: not its request
		}
		id := relayAnswerID(call.ID, rl.host.ID)
		var seen uiAnswer
		if a.db.Get(uiAnswersTable.Name, id, &seen) == nil {
			continue
		}
		ans := a.runRelayed(call)
		ans.ID, ans.Call, ans.Host, ans.At = id, call.ID, rl.host.ID, rl.now().UnixNano()
		if err := a.db.Tx([]string{uiAnswersTable.Name}, func(tx *store.Tx) error { return tx.Put(uiAnswersTable.Name, id, ans) }); err != nil {
			return err
		}
	}
	if len(stale) > 0 {
		if err := a.dropRelayCalls(stale); err != nil {
			return err
		}
	}
	// The token read before the rows: a request written since moves it, so
	// the next look reads again (this look's own deletes do too, once).
	rl.tok = tok
	return nil
}

// runRelayed runs one call as its caller, as a socket call would.
func (a *App) runRelayed(call uiCall) uiAnswer {
	reply := make(chan ochazuke.Response, 1)
	a.r.HandleCall(ochazuke.CallMsg{Actor: call.Actor, Headless: true, Source: uiRelaySrc, Request: call.Request, Reply: reply})
	resp := <-reply
	ans := uiAnswer{Error: resp.Error, Outcome: resp.Outcome}
	if ans.Outcome != nil {
		ans.Outcome.Value = nil // the value is Result
	}
	if resp.Result != nil {
		if data, err := json.Marshal(resp.Result); err == nil {
			ans.Result = data
		}
	}
	return ans
}

// dropRelayCalls drops requests and every answer to them.
func (a *App) dropRelayCalls(ids []string) error {
	answers, err := a.db.All(uiAnswersTable.Name)
	if err != nil {
		return err
	}
	return a.db.Tx([]string{uiCallsTable.Name, uiAnswersTable.Name}, func(tx *store.Tx) error {
		for _, raw := range answers {
			var ans uiAnswer
			if json.Unmarshal(raw, &ans) == nil && slices.Contains(ids, ans.Call) {
				if err := tx.Delete(uiAnswersTable.Name, ans.ID); err != nil {
					return err
				}
			}
		}
		for _, id := range ids {
			if err := tx.Delete(uiCallsTable.Name, id); err != nil {
				return err
			}
		}
		return nil
	})
}

func relayAnswerID(call, host string) string { return call + "|" + host }

func relayID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
