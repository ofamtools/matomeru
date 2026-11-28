package data

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"ofam.dev/ochazuke/core"
)

// Ideas are things the owner wants to explore that are not set in stone. An
// idea is a concept with conceptStatus "idea" (the canonical noun), or any
// record explicitly marked as one. Ideas are never picked up by agents: the
// queue, dispatch and triage boundaries all refuse them until the owner
// promotes the idea into an actionable task.
const (
	// IdeaMarker is the work status or boolean flag that marks a non-concept
	// record (for example a task) as an idea.
	IdeaMarker = "idea"
	// FieldIdeaProject links an idea to the project (root task) it is about.
	FieldIdeaProject = "project"
	// FieldIdeaPromotedTask records the task an idea was promoted into.
	FieldIdeaPromotedTask = "promotedTask"
	// RelPromotedFrom links a promoted task back to its originating idea.
	RelPromotedFrom = RelDerivedFrom

	// IdeaSkipReason is the reason given whenever an agent path skips an idea.
	IdeaSkipReason = "is an idea (exploration only): agents skip ideas until you promote it"
)

// ErrIdeaNotDispatchable is returned when anything tries to queue, dispatch
// or hand an idea to an agent.
var ErrIdeaNotDispatchable = errors.New("data: ideas are exploration only and never go to agents; promote the idea first")

// IsIdea reports whether rec is a non-actionable idea.
func IsIdea(rec Record) bool {
	if rec.Kind == KindConcept {
		return strings.TrimSpace(rec.Text(FieldConceptStatus)) == ConceptStatusIdea
	}
	return strings.TrimSpace(rec.Text("workStatus")) == IdeaMarker || rec.Bool(IdeaMarker)
}

// CanAutoDispatch reports whether an agent queue, dispatch or auto-triage may
// pick rec up. Ideas never may.
func CanAutoDispatch(rec Record) bool { return !IsIdea(rec) }

// IdeaCaptureInput is the content of a new idea.
type IdeaCaptureInput struct {
	Title     string `json:"title"`
	Summary   string `json:"summary,omitempty"`
	Body      string `json:"body,omitempty"`
	ProjectID string `json:"project,omitempty"`
}

// CaptureIdeaInput validates in and returns the CreateInput of the concept.
// The record is never flagged for an agent.
func CaptureIdeaInput(in IdeaCaptureInput) (CreateInput, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return CreateInput{}, errors.New("data: an idea needs a title")
	}
	fields := map[string]any{
		"title":            title,
		FieldConceptStatus: ConceptStatusIdea,
	}
	if s := strings.TrimSpace(in.Summary); s != "" {
		fields["summary"] = s
	}
	if strings.TrimSpace(in.Body) != "" {
		fields["body"] = in.Body
	}
	if p := strings.TrimSpace(in.ProjectID); p != "" {
		fields[FieldIdeaProject] = core.Ref{Kind: KindTask, ID: p}
	}
	return CreateInput{Kind: KindConcept, Fields: fields}, nil
}

// CaptureIdea stores a new idea concept.
func (s *Store) CaptureIdea(in IdeaCaptureInput, actor core.Actor) (*Record, error) {
	create, err := CaptureIdeaInput(in)
	if err != nil {
		return nil, err
	}
	rec, _, err := s.Create(create, actor)
	return rec, err
}

// PromoteIdeaInput returns the fields of the actionable task an idea becomes:
// a todo task in the idea's project, not flagged for agents. Promotion only
// makes the work actionable; queueing it stays a separate owner choice.
func PromoteIdeaInput(idea Record) (CreateInput, error) {
	if !IsIdea(idea) {
		return CreateInput{}, fmt.Errorf("data: %s is not an idea", RefText(idea.Ref()))
	}
	title := strings.TrimSpace(idea.Text("title"))
	if title == "" {
		return CreateInput{}, errors.New("data: the idea has no title to promote")
	}
	desc := strings.TrimSpace(idea.Text("body"))
	if desc == "" {
		desc = strings.TrimSpace(idea.Text("summary"))
	}
	fields := map[string]any{"title": title, "workStatus": "todo", "forAgent": false}
	if desc != "" {
		fields["description"] = desc
	}
	if project, ok := idea.RefField(FieldIdeaProject); ok && project.ID != "" {
		fields["parent"] = core.Ref{Kind: KindTask, ID: project.ID}
	}
	return CreateInput{Kind: KindTask, Fields: fields}, nil
}

// PromoteIdea turns the idea ideaID into a task in one change set: the task
// links back to the idea (provenance), and the idea becomes active and
// records the task it was promoted into.
func (s *Store) PromoteIdea(ideaID string, actor core.Actor) (*Record, error) {
	ideaID = strings.TrimSpace(ideaID)
	if ideaID == "" {
		return nil, errors.New("data: idea ID is required")
	}
	ideaRef := core.Ref{Kind: KindConcept, ID: ideaID}
	var task *Record
	_, err := s.write(actor, "idea.promote", "promote "+RefText(ideaRef), 0, false, func(t *txn) error {
		idea, err := s.loadRecord(t.tx, ideaRef)
		if err != nil {
			return err
		}
		create, err := PromoteIdeaInput(*idea)
		if err != nil {
			return err
		}
		ref := core.Ref{Kind: KindTask, ID: newID()}
		if err := t.createRecord(ref, create, actor); err != nil {
			return err
		}
		if err := t.apply(FieldChange{Ref: ref, Op: OpRelate, New: Relation{From: ref, Kind: RelPromotedFrom, To: ideaRef}}); err != nil {
			return err
		}
		if err := t.patch(PatchInput{Ref: ideaRef, Fields: map[string]any{
			FieldConceptStatus:    ConceptStatusActive,
			FieldIdeaPromotedTask: ref,
		}}); err != nil {
			return err
		}
		task, err = s.loadRecord(t.tx, ref)
		return err
	})
	if err != nil {
		return nil, err
	}
	return task, nil
}

// Ideas returns the open ideas (optionally only those of projectID), most
// recently updated first.
func (s *Store) Ideas(projectID string) ([]Record, error) {
	res, err := s.Query(Query{Kind: KindConcept})
	if err != nil {
		return nil, err
	}
	return FilterIdeas(res.Items, projectID), nil
}

// FilterIdeas keeps the unarchived ideas of recs (in projectID when it is
// set), ordered by updated time, newest first.
func FilterIdeas(recs []Record, projectID string) []Record {
	projectID = strings.TrimSpace(projectID)
	var out []Record
	for _, rec := range recs {
		if rec.IsArchived() || !IsIdea(rec) {
			continue
		}
		if projectID != "" {
			if p, ok := rec.RefField(FieldIdeaProject); !ok || p.ID != projectID {
				continue
			}
		}
		out = append(out, rec)
	}
	slices.SortStableFunc(out, func(a, b Record) int {
		if c := ideaTime(b).Compare(ideaTime(a)); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

func ideaTime(rec Record) time.Time {
	if !rec.UpdatedAt.IsZero() {
		return rec.UpdatedAt
	}
	return rec.CreatedAt
}

// checkAgentFlag validates an agent status and refuses to flag an idea for
// agent work.
func (s *Store) checkAgentFlag(ref core.Ref, forAgent bool, status string) error {
	switch status {
	case "", AgentStatusNone:
	case AgentStatusQueued, AgentStatusWorking, AgentStatusNeedsYou, AgentStatusDone:
		forAgent = true
	default:
		return fmt.Errorf("data: invalid agentStatus %q", status)
	}
	if !forAgent {
		return nil
	}
	return s.guardAgentFlag(ref)
}

// guardAgentFlag refuses to flag an idea for agent work.
func (s *Store) guardAgentFlag(ref core.Ref) error {
	rec, err := s.Get(ref)
	if err != nil {
		return err
	}
	if IsIdea(*rec) {
		return fmt.Errorf("%w (%s)", ErrIdeaNotDispatchable, RefText(ref))
	}
	return nil
}

// PruneIdeas returns node without its idea descendants, so a subtree handed
// to an agent never carries ideas.
func PruneIdeas(node TreeNode) TreeNode {
	if len(node.Children) == 0 {
		return node
	}
	kept := make([]TreeNode, 0, len(node.Children))
	for _, child := range node.Children {
		if IsIdea(child.Record) {
			continue
		}
		kept = append(kept, PruneIdeas(child))
	}
	node.Children = kept
	return node
}
