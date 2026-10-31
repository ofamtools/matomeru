package history

import (
	"cmp"
	"slices"
	"strings"
)

// Response is one answer the agent gave inside a turn.
type Response struct {
	ID   string `json:"id"`
	At   int64  `json:"at"`
	Text string `json:"text"`
}

// PaneTurn is one turn projected from the journal (matomeru-old's
// reviews.Review of kind turn): the prompt that started it, every answer
// in it, and the owner the pane had when the prompt went in, which a
// later move does not change.
type PaneTurn struct {
	ID          string     `json:"id"` // the prompt entry's id; review marks key on it
	Session     string     `json:"session"`
	Window      string     `json:"window"`
	Pane        string     `json:"pane,omitempty"`
	Harness     string     `json:"harness,omitempty"`
	TaskID      string     `json:"task_id,omitempty"`
	ProjectID   string     `json:"project_id,omitempty"`
	Prompt      string     `json:"prompt"`
	Responses   []Response `json:"responses"`
	StartedAt   int64      `json:"started_at"`
	CompletedAt int64      `json:"completed_at,omitempty"` // 0 while the agent is still on it
	Captured    bool       `json:"captured,omitempty"`     // a capture hook recorded it in full
	ReviewedAt  int64      `json:"reviewed_at,omitempty"`
}

// Response is the turn's answers, one after another.
func (t PaneTurn) Response() string {
	texts := make([]string, len(t.Responses))
	for i, r := range t.Responses {
		texts[i] = r.Text
	}
	return strings.Join(texts, "\n\n")
}

// Done reports a completed turn: answered, and not still running.
func (t PaneTurn) Done() bool { return t.CompletedAt != 0 && len(t.Responses) > 0 }

// echo is how close (milliseconds) the daemon's prompt summary of a
// captured turn lands to the capture's own start: both hooks run on the
// same prompt, each within its 5 second timeout.
const echo = 5_000

// echoesOf is the daemon prompts that are summaries of captured starts:
// each captured start takes the nearest daemon prompt of its pane within
// echo, at most one, and a daemon prompt echoes at most one start.
func echoesOf(entries []Entry) map[string]bool {
	type paneKey struct{ session, window string }
	daemon := map[paneKey][]Entry{}
	for _, e := range entries {
		if e.Turn == nil && e.Kind == "prompt-submitted" {
			k := paneKey{e.Session, e.Window}
			daemon[k] = append(daemon[k], e)
		}
	}
	out := map[string]bool{}
	for _, e := range entries {
		if e.Turn == nil || e.Kind != "prompt-submitted" {
			continue
		}
		best, gap := "", int64(echo+1)
		for _, d := range daemon[paneKey{e.Session, e.Window}] {
			if g := abs(d.At - e.At); g < gap && !out[d.ID] {
				best, gap = d.ID, g
			}
		}
		if best != "" {
			out[best] = true
		}
	}
	return out
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// Turns projects the journal into turns, oldest first by start; an empty
// window selects every pane. It follows matomeru-old's reviews.project:
//
//   - a prompt starts a turn and the answers after it belong to it;
//   - a <task-notification> prompt continues the open turn (it reopens it
//     until the next answer);
//   - a prompt while the open turn has no answer yet continues it too (a
//     prompt typed while the agent works), its text added to the turn's;
//   - a subagent running keeps the turn open; a session start ends it;
//   - a turn keeps the owner journaled with its prompt.
//
// A captured turn (mtm's capture hooks, with the whole prompt and answer)
// wins over the daemon's summaries of the same turn: the daemon's prompt
// that echoes a captured start (echoesOf), and its answers while a
// captured turn is the open one, are left out. Marks give each turn its ReviewedAt.
func (j *Journal) Turns(window string) ([]PaneTurn, error) {
	entries, err := j.snapshot()
	if err != nil {
		return nil, err
	}
	marks, err := j.Marks()
	if err != nil {
		return nil, err
	}
	return project(entries, window, marks), nil
}

func project(entries []Entry, window string, marks map[string]int64) []PaneTurn {
	type paneKey struct{ session, window string }
	echoes := echoesOf(entries)
	type state struct {
		turn     int
		awaiting bool
		agents   int
	}
	active := map[paneKey]*state{}
	captured := map[string]int{} // a captured turn's key → its turn
	var turns []PaneTurn
	for _, e := range entries {
		if e.Window == "" || (window != "" && e.Window != window) {
			continue
		}
		k := paneKey{e.Session, e.Window}
		st := active[k]
		if st == nil {
			st = &state{turn: -1}
			active[k] = st
		}
		key := ""
		if e.Turn != nil {
			key = turnKey(*e.Turn)
		}
		switch e.Kind {
		case "session-start", "session_start":
			*st = state{turn: -1}
		case "prompt-submitted":
			if echoes[e.ID] {
				continue
			}
			text := entryText(e)
			open := st.turn >= 0
			switch {
			case open && Notification(text):
				turns[st.turn].CompletedAt = 0
			case open && st.awaiting && len(turns[st.turn].Responses) == 0 && (e.Turn == nil) == !turns[st.turn].Captured:
				if text != "" {
					turns[st.turn].Prompt = strings.TrimSpace(turns[st.turn].Prompt + "\n\n" + text)
				}
			case Notification(text):
				continue
			default:
				st.turn = len(turns)
				turns = append(turns, PaneTurn{ID: e.ID, Session: e.Session, Window: e.Window, Pane: e.Pane, Harness: e.Harness,
					TaskID: e.TaskID, ProjectID: e.ProjectID, Prompt: text, StartedAt: e.At, Captured: e.Turn != nil,
					ReviewedAt: marks[e.ID], Responses: []Response{}})
			}
			st.awaiting = true
			if key != "" {
				captured[key] = st.turn
			}
		case "response-completed":
			index := st.turn
			if key != "" {
				var ok bool
				if index, ok = captured[key]; !ok {
					continue
				}
			} else if index >= 0 && turns[index].Captured {
				continue
			}
			if index < 0 {
				continue
			}
			if index == st.turn {
				st.awaiting = false
			}
			t := &turns[index]
			if e.Turn != nil && len(e.Turn.Prompt) > len(t.Prompt) && strings.Contains(e.Turn.Prompt, t.Prompt) {
				t.Prompt = e.Turn.Prompt // the completion carries every prompt the turn took
			}
			t.Responses = append(t.Responses, Response{ID: e.ID, At: e.At, Text: entryText(e)})
			if st.agents == 0 {
				t.CompletedAt = e.At
			}
		case "subagent-start", "subagent_start":
			st.agents++
			if st.turn >= 0 {
				turns[st.turn].CompletedAt = 0
			}
		case "subagent-stop", "subagent_stop":
			st.agents = max(0, st.agents-1)
			if st.turn >= 0 && !st.awaiting && st.agents == 0 && len(turns[st.turn].Responses) > 0 {
				turns[st.turn].CompletedAt = e.At
			}
		}
	}
	slices.SortStableFunc(turns, func(a, b PaneTurn) int { return cmp.Compare(a.StartedAt, b.StartedAt) })
	return turns
}

func entryText(e Entry) string {
	if e.Detail != "" {
		return e.Detail
	}
	return e.Summary
}
