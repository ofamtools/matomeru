// Package history is mtm's append-only journal of agent activity, lifted
// from matomeru-old (internal/history): the daemon's agent-activity events
// as they arrive, and the full turns mtm's own capture hooks record
// through the CLI (history capture-turn). Rows are matomeru-old's Entry, in
// its history table, so the turns the old app journaled read back
// unchanged. Turns are projected from the journal (Turns); only review
// marks are stored beside it (marks.go).
package history

import (
	"cmp"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/unixsuperhero/matomeru/store"
)

// Table is the journal, declared exactly as matomeru-old declared it (an
// existing database keeps its rows): owner history, key id, version 1,
// append-only, so the database refuses every update and delete.
var Table = store.Table{Name: "history", Owner: "history", Key: "id", Version: 1, AppendOnly: true}

// Entry is one journal row (matomeru-old's history.Entry, field for field).
type Entry struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	At        int64    `json:"at"`
	Observed  int64    `json:"observed"`
	Source    string   `json:"source"`
	SourceID  string   `json:"source_id"`
	Sequence  uint64   `json:"sequence,omitempty"`
	Session   string   `json:"session,omitempty"`
	Window    string   `json:"window,omitempty"`
	TaskID    string   `json:"task_id,omitempty"`
	ProjectID string   `json:"project_id,omitempty"`
	Pane      string   `json:"pane,omitempty"`
	Harness   string   `json:"harness,omitempty"`
	AgentID   string   `json:"agent_id,omitempty"`
	Cwd       string   `json:"cwd,omitempty"`
	Tool      string   `json:"tool,omitempty"`
	Target    string   `json:"target,omitempty"`
	Files     []string `json:"files,omitempty"`
	OK        *bool    `json:"ok,omitempty"`
	Summary   string   `json:"summary"`
	Detail    string   `json:"detail"`
	Turn      *Turn    `json:"turn,omitempty"`
	// Provenance links the entry to its turn, pane, task and project.
	Provenance Provenance `json:"provenance"`
}

// Provenance is matomeru-old's core.Provenance: the turn, pane, task and
// project an entry belongs to, and where the pane's repository stood.
type Provenance struct {
	TurnID    string `json:"turn_id,omitempty"`
	Session   string `json:"session,omitempty"`
	Window    string `json:"window,omitempty"`
	TaskID    string `json:"task_id,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
	Commit    string `json:"commit,omitempty"`
	Branch    string `json:"branch,omitempty"`
	PRURL     string `json:"pr_url,omitempty"`
	At        int64  `json:"at"`
}

// Owner names the task and project a pane belongs to now; an entry keeps
// the owner it was journaled with, so a later move never moves it.
type Owner func(session, window string) (taskID, projectID string, err error)

// Journal is the history table.
type Journal struct {
	db    *store.Store
	owner Owner
}

// Open declares the journal and the review marks (marks.go) and returns
// the unit. owner may be nil (entries are then journaled unowned).
func Open(db *store.Store, owner Owner) (*Journal, error) {
	for _, t := range []store.Table{Table, MarksTable} {
		if err := db.Declare(t); err != nil {
			return nil, err
		}
	}
	return &Journal{db: db, owner: owner}, nil
}

// Now is the journal's clock, Unix milliseconds; tests replace it.
var Now = func() int64 { return time.Now().UnixMilli() }

// Append journals entries that are not there yet (an entry's id is a hash
// of where it came from, so a replayed event is a no-op), in one
// transaction, and returns the ids it wrote. An entry with no owner gets
// the pane's owner now.
func (j *Journal) Append(entries ...Entry) ([]string, error) {
	var ids []string
	for i := range entries {
		j.own(&entries[i])
	}
	err := j.db.Tx([]string{Table.Name}, func(tx *store.Tx) error {
		ids = ids[:0]
		for _, e := range entries {
			var existing Entry
			err := tx.Get(Table.Name, e.ID, &existing)
			if err == nil {
				continue
			}
			if !errors.Is(err, store.ErrNotFound) {
				return err
			}
			if err := tx.Put(Table.Name, e.ID, e); err != nil {
				return err
			}
			ids = append(ids, e.ID)
		}
		return nil
	})
	return ids, err
}

// own fills an unowned entry's task and project from the pane's owner now.
func (j *Journal) own(e *Entry) {
	if j.owner == nil || e.Window == "" || e.TaskID != "" || e.ProjectID != "" {
		return
	}
	if task, project, err := j.owner(e.Session, e.Window); err == nil {
		e.TaskID, e.ProjectID = task, project
		e.Provenance.TaskID, e.Provenance.ProjectID = task, project
	}
}

// Get is one entry by id.
func (j *Journal) Get(id string) (Entry, error) {
	var e Entry
	err := j.db.Get(Table.Name, id, &e)
	return e, err
}

// All is every entry, in matomeru-old's order: by time, then observation,
// source, session, window, sequence and source id.
func (j *Journal) All() ([]Entry, error) {
	rows, err := j.db.All(Table.Name)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(rows))
	for _, row := range rows {
		var e Entry
		if err := json.Unmarshal(row, &e); err != nil {
			return nil, fmt.Errorf("decode history: %w", err)
		}
		entries = append(entries, e)
	}
	slices.SortStableFunc(entries, func(a, b Entry) int {
		return cmp.Or(cmp.Compare(a.At, b.At), cmp.Compare(a.Observed, b.Observed), cmp.Compare(a.Source, b.Source),
			cmp.Compare(a.Session, b.Session), cmp.Compare(a.Window, b.Window), cmp.Compare(a.Sequence, b.Sequence), cmp.Compare(a.SourceID, b.SourceID))
	})
	return entries, nil
}

// LastSequence is the highest activity sequence journaled for a pane under
// a daemon boot: a ring read resumes after it.
func (j *Journal) LastSequence(boot, window string) (uint64, error) {
	entries, err := j.All()
	if err != nil {
		return 0, err
	}
	prefix := boot + "/" + window + "/activity:"
	var last uint64
	for _, e := range entries {
		if e.Window == window && strings.HasPrefix(e.SourceID, prefix) {
			last = max(last, e.Sequence)
		}
	}
	return last, nil
}

// Activity is one entry of a pane's activity ring (the daemon's
// agent-activity verb and event), the fields the journal keeps.
type Activity struct {
	Seq    uint64
	At     int64 // Unix nanoseconds, as the daemon sends it
	Kind   string
	Tool   string
	Target string
	Files  []string
	OK     *bool
	Text   string
}

// Pane is what the journal knows about the pane an activity came from.
type Pane struct {
	Session, Window, Name, Harness, Cwd string
}

// FromActivity is the journal entry of one ring entry (matomeru-old's
// normalize for agent-activity): its kind renamed (prompt is
// prompt-submitted, turn_end response-completed), its id a hash of the
// boot, the window and the ring sequence, so the live event and a later
// ring read of the same entry are one row. ok is false for an entry the
// journal does not keep: one without a kind, and the prompt and turn_end
// summaries mtm's own capture reported (target mtm-turn:…), whose full
// turn is journaled by the capture itself.
func FromActivity(boot string, p Pane, a Activity) (Entry, bool) {
	if a.Kind == "" || (a.Kind == "prompt" || a.Kind == "turn_end") && strings.HasPrefix(a.Target, "mtm-turn:") {
		return Entry{}, false
	}
	kind := map[string]string{"prompt": "prompt-submitted", "turn_end": "response-completed", "session_start": "session-start",
		"subagent_start": "subagent-start", "subagent_stop": "subagent-stop"}[a.Kind]
	if kind == "" {
		kind = a.Kind
	}
	e := Entry{Kind: kind, At: a.At, Observed: Now(), Source: "daemon", Session: p.Session, Window: p.Window, Pane: p.Name,
		Harness: p.Harness, Cwd: p.Cwd, Tool: a.Tool, Target: a.Target, Files: a.Files, OK: a.OK, Detail: a.Text, Sequence: a.Seq}
	if e.Detail == "" {
		e.Detail = strings.TrimSpace(a.Tool + " " + a.Target)
	}
	e.SourceID = boot + "/" + p.Window + "/activity:" + strconv.FormatUint(a.Seq, 10)
	if a.Seq == 0 {
		e.SourceID = boot + "/" + p.Window + "/activity:" + newID()
	}
	// tuios activity uses Unix nanoseconds; the journal milliseconds.
	if e.At >= 1e17 {
		e.At /= int64(time.Millisecond)
	}
	if e.At <= 0 {
		e.At = e.Observed
	}
	e.Summary = summary(e.Detail)
	if e.Summary == "" {
		e.Summary = e.Kind
	}
	e.ID = entryID(e.Source, e.Session, e.Window, e.SourceID)
	e.Provenance = Provenance{Session: e.Session, Window: e.Window, At: e.At}
	if e.Kind == "prompt-submitted" && !Notification(e.Detail) {
		e.Provenance.TurnID = e.ID
	}
	return e, true
}

// entryID is matomeru-old's entry id: a hash of where the entry came from.
func entryID(source, session, window, sourceID string) string {
	key := sha256.Sum256([]byte(source + "\x00" + session + "\x00" + window + "\x00" + sourceID))
	return hex.EncodeToString(key[:])
}

// summary is a text's first line, cut to 160 runes.
func summary(text string) string {
	s := strings.TrimSpace(strings.SplitN(text, "\n", 2)[0])
	if runes := []rune(s); len(runes) > 160 {
		s = string(runes[:157]) + "..."
	}
	return s
}

// Notification reports a <task-notification> prompt, which continues the
// open turn instead of starting one (matomeru-old's
// concept.review.notification-continues-turn).
func Notification(text string) bool {
	text = strings.TrimSpace(text)
	return strings.HasPrefix(text, "<task-notification>") || strings.HasPrefix(text, "<task-notification ") || strings.HasPrefix(text, "<task-notification\n")
}

func newID() string {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(id[:])
}

// NewID is a fresh turn id.
func NewID() string { return newID() }
