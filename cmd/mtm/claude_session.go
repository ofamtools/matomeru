package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/unixsuperhero/matomeru/internal/turns"
	"github.com/unixsuperhero/matomeru/internal/work"
)

// A Claude Code pane's current transcript, for the Turns tab
// (sessionLogFinder) and a side's folder (agentCwd), is one rule:
// turns.ResolveClaudeLog. The session id tuios reports wins when its
// transcript exists. After a resume or restart tuios can keep an id that
// has no transcript; then the one transcript in the pane's folder written
// recently and after the pane's claude process started is taken, unless
// another pane owns it or another Claude pane in the folder is unresolved
// too. When unsure the tab shows mtm's journal and the side the pane's
// folder, as with no transcript at all. Upstream fix: tuios should refresh
// agent_session_id from the agent's SessionStart hook on resume and
// restart; then the fallback never runs.

// claudeLookup is the lookup of p's current transcript against the
// listing (the other panes) and the recorded sessions. Run off the loop:
// it reads the store and stats transcripts.
func claudeLookup(projects string, ws *work.Store, p pane, listing []pane, started func(pane) time.Time) turns.ClaudeLookup {
	q := turns.ClaudeLookup{Projects: projects, Cwd: p.Cwd, Session: p.AgentSession}
	if started != nil {
		q.Started = func() time.Time { return started(p) }
	}
	if ws != nil {
		recs, err := ws.AllPaneSessions()
		if err != nil {
			// Not knowing who owns what is not knowing: no fallback.
			q.Started = nil
		}
		for _, rec := range recs {
			if rec.Window != p.ID {
				q.Claimed = append(q.Claimed, rec.Sessions...)
			}
		}
	}
	dir := turns.ClaudeProjectDir(p.Cwd)
	for _, o := range listing {
		if o.ID == p.ID {
			continue
		}
		if o.AgentSession != "" {
			q.Claimed = append(q.Claimed, o.AgentSession)
		}
		if o.Exited || !isClaude(o) || turns.ClaudeProjectDir(o.Cwd) != dir {
			continue
		}
		if _, err := turns.FindClaudeLog(projects, o.Cwd, o.AgentSession); err != nil {
			q.Contested = true
		}
	}
	return q
}

// agentStartUnknownFor is how long "no claude process found" stands
// before ps is asked again: the agent may not have started yet.
const agentStartUnknownFor = 30 * time.Second

// agentStarts caches each pane's agent start (processAgentStart), so the
// Turns tab's polls do not run ps every time. An entry stands while the
// pane's process (pid) and reported session are the same; a found start
// stands until then, "none found" for agentStartUnknownFor. forget drops a
// pane's entry (pane.refresh-turns). Safe for concurrent use: lookups run
// off the loop.
type agentStarts struct {
	mu   sync.Mutex
	now  func() time.Time
	byID map[string]agentStart
}

// agentStart is one pane's cached start: the process and session it was
// looked up for, the start (zero: none found) and when it was looked up.
type agentStart struct {
	pid     int
	session string
	start   time.Time
	at      time.Time
}

// started is p's agent start through lookup, cached.
func (c *agentStarts) started(p pane, lookup func(pane) time.Time) time.Time {
	if lookup == nil {
		return time.Time{}
	}
	now := time.Now
	if c.now != nil {
		now = c.now
	}
	c.mu.Lock()
	e, ok := c.byID[p.ID]
	c.mu.Unlock()
	if ok && e.pid == p.PID && e.session == p.AgentSession && (!e.start.IsZero() || now().Sub(e.at) < agentStartUnknownFor) {
		return e.start
	}
	start := lookup(p)
	c.mu.Lock()
	if c.byID == nil {
		c.byID = map[string]agentStart{}
	}
	c.byID[p.ID] = agentStart{pid: p.PID, session: p.AgentSession, start: start, at: now()}
	c.mu.Unlock()
	return start
}

// forget drops window's cached start.
func (c *agentStarts) forget(window string) {
	c.mu.Lock()
	delete(c.byID, window)
	c.mu.Unlock()
}

// agentStartedFunc is the cached agent start lookup for claudeLookup, made
// on the loop (it reads a.agentStarted) for use off it.
func (a *App) agentStartedFunc() func(pane) time.Time {
	lookup, cache := a.agentStarted, a.starts
	if lookup == nil {
		return nil
	}
	if cache == nil {
		return lookup
	}
	return func(p pane) time.Time { return cache.started(p, lookup) }
}

// processAgentStart is when p's Claude Code process started, from the
// process table: the pane's own process when it is claude, else its one
// outermost claude descendant. Zero when there is none, more than one, or
// ps cannot say.
func processAgentStart(p pane) time.Time {
	if p.PID <= 0 {
		return time.Time{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-A", "-o", "pid=,ppid=,etime=,comm=").Output()
	if err != nil {
		return time.Time{}
	}
	return agentStartFrom(string(out), p.PID, time.Now())
}

// agentStartFrom is processAgentStart over ps's pid, ppid, etime and comm
// columns.
func agentStartFrom(table string, root int, now time.Time) time.Time {
	type proc struct {
		ppid    int
		elapsed time.Duration
		claude  bool
	}
	procs := map[int]proc{}
	children := map[int][]int{}
	for _, line := range strings.Split(table, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		elapsed, ok := parseEtime(f[2])
		if err1 != nil || err2 != nil || !ok {
			continue
		}
		comm := filepath.Base(strings.Join(f[3:], " "))
		procs[pid] = proc{ppid: ppid, elapsed: elapsed, claude: comm == "claude"}
		children[ppid] = append(children[ppid], pid)
	}
	if pr, ok := procs[root]; ok && pr.claude {
		return now.Add(-pr.elapsed)
	}
	// The outermost claude processes under root: a claude's own claude
	// children are its, not another agent.
	var found []proc
	queue := slices.Clone(children[root])
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if pr := procs[pid]; pr.claude {
			found = append(found, pr)
			continue
		}
		queue = append(queue, children[pid]...)
	}
	if len(found) != 1 {
		return time.Time{}
	}
	return now.Add(-found[0].elapsed)
}

// parseEtime reads ps's etime, [[dd-]hh:]mm:ss.
func parseEtime(s string) (time.Duration, bool) {
	days := 0
	if d, rest, ok := strings.Cut(s, "-"); ok {
		n, err := strconv.Atoi(d)
		if err != nil {
			return 0, false
		}
		days, s = n, rest
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	total := 0
	for _, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return 0, false
		}
		total = total*60 + n
	}
	return time.Duration(days)*24*time.Hour + time.Duration(total)*time.Second, true
}
