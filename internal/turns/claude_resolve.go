package turns

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// ClaudeFallbackRecent bounds the stale-id fallback: a transcript last
// written longer ago than this is not taken for a pane's current one.
const ClaudeFallbackRecent = 6 * time.Hour

// ClaudeLookup is what ResolveClaudeLog needs to find the transcript of a
// pane's current Claude Code session.
type ClaudeLookup struct {
	Projects string // Claude's projects directory (ClaudeProjects)
	Cwd      string // the pane's folder: its transcripts are in ClaudeProjectDir(Cwd)
	Session  string // the session id tuios reports for the pane
	// Started is when the pane's agent process started; it is asked only
	// when the fallback has a candidate. Nil, or a zero time, is unknown:
	// no fallback.
	Started func() time.Time
	// Claimed are the sessions other panes own (the ids tuios reports for
	// them and the ones recorded for them): never this pane's transcript.
	Claimed []string
	// Contested is set when another Claude pane in the same folder has no
	// transcript for its own reported id either: two panes could own any
	// new transcript there, so there is no fallback.
	Contested bool
	Now       time.Time // zero is time.Now
}

// ResolveClaudeLog is the transcript of a pane's current Claude Code
// session and that session's id. The id tuios reports wins whenever its
// log exists (FindClaudeLog). tuios keeps the id it first heard, so after
// a resume or a restart (claude --resume, --continue, a new process in the
// same pane) it can name a session that has no transcript while Claude
// writes another. Then, and only then, the newest transcript in the pane's
// folder is taken when every one of these holds:
//
//   - the agent's start time is known, and the transcript was written
//     after it and within ClaudeFallbackRecent;
//   - no other pane owns its session (Claimed);
//   - no other Claude pane in the folder is unresolved too (Contested);
//   - it is the only transcript that passes, so nothing is guessed
//     between two.
//
// Anything else is ErrNoLog: when unsure, show nothing. The fix belongs
// upstream: tuios should refresh agent_session_id when the agent's
// SessionStart hook reports a new id (on resume and restart).
func ResolveClaudeLog(q ClaudeLookup) (path, session string, err error) {
	if path, err := FindClaudeLog(q.Projects, q.Cwd, q.Session); err == nil {
		return path, q.Session, nil
	}
	if q.Projects == "" || q.Cwd == "" || q.Session == "" || q.Contested || q.Started == nil {
		return "", "", ErrNoLog
	}
	now := q.Now
	if now.IsZero() {
		now = time.Now()
	}
	dir := filepath.Join(q.Projects, ClaudeProjectDir(q.Cwd))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", "", ErrNoLog
	}
	type candidate struct {
		path, session string
		mod           time.Time
	}
	var found []candidate
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".jsonl")
		if !ok || e.IsDir() || id == "" || slices.Contains(q.Claimed, id) {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() || now.Sub(info.ModTime()) > ClaudeFallbackRecent {
			continue
		}
		found = append(found, candidate{filepath.Join(dir, e.Name()), id, info.ModTime()})
	}
	if len(found) == 0 {
		return "", "", ErrNoLog
	}
	started := q.Started()
	if started.IsZero() {
		return "", "", ErrNoLog
	}
	found = slices.DeleteFunc(found, func(c candidate) bool { return c.mod.Before(started) })
	if len(found) != 1 {
		return "", "", ErrNoLog
	}
	return found[0].path, found[0].session, nil
}
