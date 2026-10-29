package main

import "github.com/unixsuperhero/matomeru/internal/locate"

// A pane's agent session comes from tuios (list-agents' agent_session_id),
// which keeps the id it first heard. Each listing, off the loop, the
// harnesses' own records (harnesses.json "session", internal/locate) say
// which session each pane's agent writes now; where they name one with a
// transcript, it replaces tuios's id (kept in ReportedSession), and a pane
// tuios reports no harness for takes the record's harness. Everything that
// reads AgentSession (the Turns tab, the recorded session chain, a side's
// folder) then follows the live session; a /clear or a resume shows up on
// the next listing. Where several sessions could be the pane's, nothing is
// taken and SessionNote says so.

// locatePanes corrects panes' sessions through l (nil: as listed).
func locatePanes(panes []pane, l *locate.Locator) []pane {
	if l == nil {
		return panes
	}
	in := make([]locate.Pane, 0, len(panes))
	for _, p := range panes {
		if p.ID == "" || p.Exited {
			continue
		}
		in = append(in, locate.Pane{ID: p.ID, Harness: p.Harness, Session: p.AgentSession, Cwd: p.Cwd, TTY: p.TTY})
	}
	got := l.Locate(in)
	for i := range panes {
		f, ok := got[panes[i].ID]
		if !ok {
			continue
		}
		panes[i].SessionNote = f.Note
		if f.Session == "" {
			continue
		}
		if panes[i].Harness == "" {
			panes[i].Harness = f.Harness
		}
		if panes[i].AgentSession != f.Session {
			panes[i].ReportedSession, panes[i].AgentSession = panes[i].AgentSession, f.Session
		}
	}
	return panes
}
