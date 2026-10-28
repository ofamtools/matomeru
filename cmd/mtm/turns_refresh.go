package main

import (
	"errors"
	"time"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/turns"
)

// Refreshing a pane's turns (pane.refresh-turns, R on the Turns tab):
// a Claude pane whose transcript was not found on the first read (a resume
// before Claude wrote its new file) shows mtm's journal. Two ways out:
//
//   - pane.refresh-turns drops what the tab knows of the pane's logs (the
//     chain, the current session's resolution, the journal fallback, the
//     cached agent start), resolves the current transcript again as
//     claude_session.go says and reads the turns, then says what it found.
//   - While a pane is on the journal fallback, the listing's poll
//     (pollTurns) tries again after turnsRetry's waits, off the loop as
//     every read is; the first transcript that passes switches the tab.
//
// A side opened after either uses the resolution the tab holds (agentCwd).

// turnsRetry are the waits between the journal fallback's automatic
// retries: the first after the first miss, the last repeated.
var turnsRetry = []time.Duration{10 * time.Second, 30 * time.Second, time.Minute, 5 * time.Minute}

// turnsRetryAfter is the wait after the tries-th miss (tries from 1).
func turnsRetryAfter(tries int) time.Duration {
	i := min(max(tries, 1), len(turnsRetry)) - 1
	return turnsRetry[i]
}

// turnsResolution is what the tab knows of a pane's current session's
// log: the session tuios reported, the log's session it resolved to ("" not
// found), and the retry schedule while not found.
type turnsResolution struct {
	reported string
	session  string
	tries    int       // misses since the last find or refresh
	retryAt  time.Time // the fallback's next automatic try
}

// now is the clock the Turns tab's retries go by.
func (a *App) now() time.Time {
	if a.clock != nil {
		return a.clock()
	}
	return time.Now()
}

// logKind is where the pane's turns come from when a log is found: the
// kind turnKind gives before any fallback.
func (a *App) logKind(p pane) int {
	switch {
	case isClaude(p) && len(a.agentSessions(p)) > 0:
		return turnsClaude
	case isOmp(p) && len(a.agentSessions(p)) > 0:
		return turnsOmp
	case !p.isAgent() && p.MarksCommands:
		return turnsShell
	}
	return turnsJournal
}

// retryDue is whether a pane on the journal fallback tries its log again:
// its retry time came, or tuios reports another session.
func (a *App) retryDue(pt *paneTurns, p pane) bool {
	if !pt.fallback {
		return false
	}
	if k := a.logKind(p); k != turnsClaude && k != turnsOmp {
		return false
	}
	return pt.cur.reported != p.AgentSession || !a.now().Before(pt.cur.retryAt)
}

// resolvedSession is the session of the log the Turns tab resolved for
// window's current session, "" when it holds none for reported.
func (a *App) resolvedSession(window, reported string) string {
	pt := a.mturns[window]
	if pt == nil || pt.cur.reported != reported {
		return ""
	}
	return pt.cur.session
}

// resolutions are the Turns tab's resolutions by window, copied on the
// loop for a read off it (a side's agentCwd).
type resolutions map[string]turnsResolution

// resolvedSessions copies every pane's resolution.
func (a *App) resolvedSessions() resolutions {
	out := resolutions{}
	for id, pt := range a.mturns {
		if pt.cur.session != "" {
			out[id] = pt.cur
		}
	}
	return out
}

// of is the session p's current one resolved to, "" none (or resolved for
// a session tuios no longer reports).
func (r resolutions) of(p pane) string {
	if c, ok := r[p.ID]; ok && c.reported == p.AgentSession {
		return c.session
	}
	return ""
}

// refreshPaneTurns is pane.refresh-turns (and the turn rows' refresh-turns,
// for the pane the tab shows): the pane's logs found again from nothing,
// the turns read, and a notice of what was found.
func (a *App) refreshPaneTurns(ctx ochazuke.Ctx) ochazuke.Result {
	row := ctx.Instance
	if row == nil {
		return ochazuke.Failed(errors.New("select a pane"))
	}
	if row.Noun != "pane" {
		if row = a.r.Instances[a.pv.turnsFor]; row == nil {
			return ochazuke.Failed(errors.New("no pane's turns are shown"))
		}
	}
	p, err := a.paneAt(row)
	if err != nil {
		return ochazuke.Failed(err)
	}
	pt := a.paneTurnsOf(p.ID)
	if a.starts != nil {
		a.starts.forget(p.ID)
	}
	pt.chain, pt.cur, pt.fallback, pt.err, pt.loading = &turns.LogChain{}, turnsResolution{}, false, "", false
	switch a.logKind(p) {
	case turnsClaude, turnsOmp:
		return a.readLogTurns(p, pt, true)
	case turnsShell:
		a.r.UI().Info("turns: the shell's commands")
	default:
		a.r.UI().Info("turns: " + journalNotice)
	}
	sub := ctx
	sub.Instance = row
	return a.readPaneTurns(sub)
}

// journalNotice ends a refresh that found no log.
const journalNotice = "no transcript, showing mtm's journal"

// turnsNotice is what a refresh found: the current session's log (marked
// when tuios's id was stale), only earlier sessions' logs, or none.
func turnsNotice(reported string, got claudeRead) string {
	switch {
	case got.NoLog:
		return "turns: " + journalNotice
	case got.Session == "":
		return "turns: no transcript of session " + shortID(reported) + ", showing earlier sessions"
	case got.Session != reported:
		return "turns: transcript " + shortID(got.Session) + " (resumed session)"
	}
	return "turns: transcript " + shortID(got.Session)
}

// shortID is a session id's first 8 characters and an ellipsis.
func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8] + "…"
}
