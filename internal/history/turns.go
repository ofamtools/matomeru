package history

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/unixsuperhero/matomeru/store"
)

// Turn is one delivered user request through the agent's final answer, as
// a capture hook reports it (matomeru-old's history.Turn). A zero
// CompletedAt records the start; the completion is a separate immutable
// row.
type Turn struct {
	ID              string          `json:"id"`
	Session         string          `json:"session"`
	Window          string          `json:"window"`
	Pane            string          `json:"pane,omitempty"`
	Harness         string          `json:"harness"`
	ConversationID  string          `json:"conversation_id"`
	Prompt          string          `json:"prompt"`
	Response        string          `json:"response,omitempty"`
	StartedAt       int64           `json:"started_at"`
	CompletedAt     int64           `json:"completed_at,omitempty"`
	TranscriptPath  string          `json:"transcript_path,omitempty"`
	ResponseMessage json.RawMessage `json:"response_message,omitempty"`
}

// turnKey is the journal key of a captured turn: a hash of its identity.
func turnKey(t Turn) string {
	identity, _ := json.Marshal([]string{t.Session, t.Window, t.Harness, t.ConversationID, t.ID})
	digest := sha256.Sum256(identity)
	return "turn:" + hex.EncodeToString(digest[:])
}

// Capture journals a captured turn (matomeru-old's history.capture-turn):
// a start as a prompt-submitted entry owned by the pane's owner now, a
// completion as a response-completed entry that keeps its start's owner.
// Capturing the same turn twice is a no-op; the same id with different
// content is a conflict. It returns the entry id.
func (j *Journal) Capture(t Turn) (string, error) {
	for _, value := range []string{t.ID, t.Session, t.Window, t.Harness, t.ConversationID} {
		if strings.TrimSpace(value) == "" {
			return "", errors.New("id, session, window, harness and conversation_id are required")
		}
	}
	if t.StartedAt <= 0 || (t.CompletedAt != 0 && t.CompletedAt < t.StartedAt) {
		return "", errors.New("turn timestamps must be Unix milliseconds, with completion at or after start")
	}
	if t.CompletedAt == 0 && (t.Response != "" || len(t.ResponseMessage) != 0) {
		return "", errors.New("a response requires completed_at")
	}
	key := turnKey(t)
	e := Entry{ID: key + ":prompt", SourceID: t.ID, Source: "harness", Kind: "prompt-submitted", Session: t.Session, Window: t.Window,
		Pane: t.Pane, Harness: t.Harness, AgentID: t.ConversationID, At: t.StartedAt, Observed: Now(), Detail: t.Prompt, Turn: &t}
	if t.CompletedAt != 0 {
		var start Entry
		if err := j.db.Get(Table.Name, e.ID, &start); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return "", errors.New("capture the turn start before its completion")
			}
			return "", err
		}
		if start.At != t.StartedAt {
			return "", errors.New("turn start timestamp differs from the recorded request")
		}
		e.ID, e.Kind, e.At, e.Detail = key+":response", "response-completed", t.CompletedAt, t.Response
		e.TaskID, e.ProjectID, e.Cwd, e.Pane = start.TaskID, start.ProjectID, start.Cwd, start.Pane
		e.Provenance = start.Provenance
		e.Provenance.At = t.CompletedAt
	} else {
		j.own(&e)
		// The turn is its own provenance.
		e.Provenance = Provenance{TurnID: e.ID, Session: t.Session, Window: t.Window, TaskID: e.TaskID, ProjectID: e.ProjectID, At: t.StartedAt}
	}
	e.Summary = summary(e.Detail)
	err := j.db.Tx([]string{Table.Name}, func(tx *store.Tx) error {
		var existing Entry
		err := tx.Get(Table.Name, e.ID, &existing)
		if err == nil {
			previous, _ := json.Marshal(existing.Turn)
			incoming, _ := json.Marshal(t)
			if !bytes.Equal(previous, incoming) {
				return errors.New("turn ID already records different content")
			}
			return nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		return tx.Put(Table.Name, e.ID, e)
	})
	if err != nil {
		return "", err
	}
	return e.ID, nil
}

// OpenCapture is the newest captured start of a conversation in a pane
// that has no completion yet: what a Stop hook completes.
func (j *Journal) OpenCapture(session, window, harness, conversation string) (Turn, bool, error) {
	entries, err := j.All()
	if err != nil {
		return Turn{}, false, err
	}
	done := map[string]bool{}
	for _, e := range entries {
		if e.Source == "harness" && e.Kind == "response-completed" {
			done[strings.TrimSuffix(e.ID, ":response")] = true
		}
	}
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		t := e.Turn
		if e.Source != "harness" || e.Kind != "prompt-submitted" || t == nil {
			continue
		}
		if t.Session == session && t.Window == window && t.Harness == harness && t.ConversationID == conversation {
			if done[strings.TrimSuffix(e.ID, ":prompt")] {
				return Turn{}, false, nil
			}
			return *t, true, nil
		}
	}
	return Turn{}, false, nil
}

// Complete journals the completion of an open captured turn.
func (j *Journal) Complete(start Turn, response string, at int64) (string, error) {
	t := start
	t.Response, t.CompletedAt = response, max(at, start.StartedAt)
	return j.Capture(t)
}
