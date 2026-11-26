package data

import (
	"encoding/json"
	"slices"
	"strings"
	"time"

	"ofam.dev/ochazuke/core"
)

const (
	defaultTimelineChangeSets = 50
	timelineTextLimit         = 100
	// TimelineKindChange is the Kind of a timeline entry made from a change set.
	TimelineKindChange = "change"
	timelineKindNote   = "note"
)

// TimelineEntry is one event of a record's timeline.
type TimelineEntry struct {
	At   time.Time
	Kind string // milestone, decision, blocker, verification, note, or TimelineKindChange
	Text string
	By   string
	Ref  *core.Ref // the note, for a logged entry
}

// attachesOnlyNotes reports whether a change set only attached notes: the
// notes themselves are in the timeline already.
func (cs ChangeSet) attachesOnlyNotes() bool {
	for _, ch := range cs.Changes {
		if ch.Op != OpRelate && ch.Op != OpUnrelate {
			return false
		}
		var rel Relation
		raw := ch.New
		if ch.Op == OpUnrelate {
			raw = ch.Old
		}
		if b, err := json.Marshal(raw); err != nil || json.Unmarshal(b, &rel) != nil || rel.Kind != RelAbout {
			return false
		}
	}
	return len(cs.Changes) > 0
}

// timelineLine is the first line of s, clipped to the timeline's text limit.
func timelineLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	if r := []rune(s); len(r) > timelineTextLimit {
		return string(r[:timelineTextLimit-1]) + "…"
	}
	return s
}

// timelineEntry is the entry a logged note makes.
func (r Record) timelineEntry() TimelineEntry {
	at := r.CreatedAt
	if logged := r.Time("loggedAt"); !logged.IsZero() {
		at = logged
	}
	kind, text := r.Text("logKind"), r.Text("title")
	if k, rest, ok := strings.Cut(text, ": "); ok && (kind == "" || k == kind) && !strings.Contains(k, " ") {
		kind, text = k, rest
	}
	if kind == "" {
		kind = timelineKindNote
	}
	if text == "" {
		text = timelineLine(r.Text("body"))
	}
	ref := r.Ref()
	return TimelineEntry{At: at, Kind: kind, Text: text, Ref: &ref}
}

// timelineEntry is the entry a change set makes.
func (cs ChangeSet) timelineEntry() TimelineEntry {
	text := cs.Summary
	if text == "" {
		text = cs.Action
	}
	by := cs.Actor.Label
	if by == "" {
		by = cs.Actor.ID
	}
	return TimelineEntry{At: cs.CreatedAt, Kind: TimelineKindChange, Text: timelineLine(text), By: by}
}

// Timeline merges the notes about a record with its recent change sets,
// oldest first: what was logged and what was changed, in the order it
// happened. limit bounds the change sets read (0 reads 50).
func (s *Store) Timeline(ref core.Ref, limit int) ([]TimelineEntry, error) {
	if limit <= 0 {
		limit = defaultTimelineChangeSets
	}
	notes, err := s.RelatedRecords(ref, RelAbout, Incoming)
	if err != nil {
		return nil, err
	}
	var out []TimelineEntry
	for _, note := range notes {
		if note.Kind == KindNote && !note.IsArchived() {
			out = append(out, note.timelineEntry())
		}
	}
	sets, err := s.History(&ref, limit)
	if err != nil {
		return nil, err
	}
	for _, cs := range sets {
		if !cs.attachesOnlyNotes() {
			out = append(out, cs.timelineEntry())
		}
	}
	slices.SortStableFunc(out, func(a, b TimelineEntry) int { return a.At.Compare(b.At) })
	return out, nil
}
