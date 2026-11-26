package mcpui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

// RelateArgs are the arguments of data.relate and data.unrelate.
type RelateArgs struct {
	From     json.RawMessage `json:"from"`
	Relation string          `json:"relation"`
	To       json.RawMessage `json:"to"`
	By       string          `json:"by,omitempty"`
}

func dataRelateToolSpecs(by map[string]any) []ToolSpec {
	return []ToolSpec{
		{
			Name:        ToolRelate,
			Description: "Create a directed relation between any two records (e.g. lane --claim--> pane, lane --plan--> document, pane --directory--> directory, note --about--> task). Accepts from/to as either {kind, id} objects or \"kind:id\" strings.",
			InputSchema: object([]string{"from", "relation", "to"}, props{
				"from":     props{"description": "source ref: {kind, id} object or \"kind:id\" string"},
				"relation": text("relation kind (e.g. claim, plan, about, directory, context)"),
				"to":       props{"description": "target ref: {kind, id} object or \"kind:id\" string"},
				"by":       by,
			}),
		},
		{
			Name:        ToolUnrelate,
			Description: "Remove a directed relation between any two records. Accepts from/to as either {kind, id} objects or \"kind:id\" strings.",
			InputSchema: object([]string{"from", "relation", "to"}, props{
				"from":     props{"description": "source ref: {kind, id} object or \"kind:id\" string"},
				"relation": text("relation kind"),
				"to":       props{"description": "target ref: {kind, id} object or \"kind:id\" string"},
				"by":       by,
			}),
		},
	}
}

func parseFlexibleRef(raw json.RawMessage) (core.Ref, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return core.Ref{}, errors.New("ref is required")
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return core.Ref{}, err
		}
		kind, id, ok := strings.Cut(strings.TrimSpace(s), ":")
		if !ok || strings.TrimSpace(kind) == "" || strings.TrimSpace(id) == "" {
			return core.Ref{}, fmt.Errorf("invalid ref %q (want kind:id)", s)
		}
		return core.Ref{Kind: strings.TrimSpace(kind), ID: strings.TrimSpace(id)}, nil
	}
	var ref core.Ref
	if err := json.Unmarshal(raw, &ref); err != nil {
		return core.Ref{}, err
	}
	if strings.TrimSpace(ref.Kind) == "" || strings.TrimSpace(ref.ID) == "" {
		return core.Ref{}, fmt.Errorf("ref requires kind and id, got %+v", ref)
	}
	return ref, nil
}

// Relate creates a directed relation between any two records.
func (d *DataTools) Relate(actor core.Actor, args RelateArgs) (map[string]any, error) {
	from, err := parseFlexibleRef(args.From)
	if err != nil {
		return nil, fmt.Errorf("from: %w", err)
	}
	to, err := parseFlexibleRef(args.To)
	if err != nil {
		return nil, fmt.Errorf("to: %w", err)
	}
	relKind := strings.TrimSpace(args.Relation)
	if relKind == "" {
		return nil, errors.New("relation is required")
	}
	rel, cs, err := d.store.Relate(data.RelateInput{From: from, Kind: relKind, To: to}, actor)
	if err != nil {
		return nil, err
	}
	return map[string]any{"relation": rel, "changeSet": cs}, nil
}

// Unrelate removes a directed relation between any two records.
func (d *DataTools) Unrelate(actor core.Actor, args RelateArgs) (map[string]any, error) {
	from, err := parseFlexibleRef(args.From)
	if err != nil {
		return nil, fmt.Errorf("from: %w", err)
	}
	to, err := parseFlexibleRef(args.To)
	if err != nil {
		return nil, fmt.Errorf("to: %w", err)
	}
	relKind := strings.TrimSpace(args.Relation)
	if relKind == "" {
		return nil, errors.New("relation is required")
	}
	cs, err := d.store.Unrelate(from, relKind, to, actor)
	if err != nil {
		return nil, err
	}
	return map[string]any{"removed": true, "changeSet": cs}, nil
}
