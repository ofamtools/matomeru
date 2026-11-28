package mcpui

import (
	"encoding/json"
	"fmt"

	agentcfg "ofam.dev/matomeru/internal/config"
)

// Agent profile and prompt segment tool names.
const (
	ToolAgentProfiles = "agent.profiles"
	ToolAgentSegments = "agent.segments"
	ToolAgentCompose  = "agent.compose"
)

// AgentProfileTools serves read-only lookups over agent_profiles.json and
// prompt_segments.json (built-ins overlaid by the config directory's files)
// and resolves a segment plus tags to a ComposedDispatch. It needs no store.
type AgentProfileTools struct {
	// ConfigDir overrides the config directory; empty resolves it from
	// Getenv (MTM_CONFIG_DIR, XDG_CONFIG_HOME, ~/.config/matomeru).
	ConfigDir string
	Getenv    func(string) string
}

// AgentProfilesArgs are the arguments of agent.profiles.
type AgentProfilesArgs struct {
	ID              string `json:"id,omitempty"`
	Role            string `json:"role,omitempty"`
	IncludeDisabled bool   `json:"includeDisabled,omitempty"`
}

// AgentSegmentsArgs are the arguments of agent.segments.
type AgentSegmentsArgs struct {
	ID   string `json:"id,omitempty"`
	Kind string `json:"kind,omitempty"`
}

// AgentComposeArgs are the arguments of agent.compose.
type AgentComposeArgs struct {
	Segment string   `json:"segment,omitempty"`
	Tags    []string `json:"tags,omitempty"`
	Prompt  string   `json:"prompt,omitempty"`
}

// AgentProfilesResult lists profiles.
type AgentProfilesResult struct {
	Default  string                  `json:"default,omitempty"`
	Roles    []string                `json:"roles"`
	Levels   []string                `json:"reasoningLevels"`
	Profiles []agentcfg.AgentProfile `json:"profiles"`
	Warning  string                  `json:"warning,omitempty"`
}

// AgentSegmentsResult lists prompt segments.
type AgentSegmentsResult struct {
	Default  string                   `json:"default,omitempty"`
	Segments []agentcfg.PromptSegment `json:"segments"`
	Warning  string                   `json:"warning,omitempty"`
}

// ToolSpecs returns the MCP tool schemas.
func (t *AgentProfileTools) ToolSpecs() []ToolSpec {
	return []ToolSpec{
		{
			Name:        ToolAgentProfiles,
			Description: "List the standard agent profiles (role, harness, provider, model, reasoning level, prompts): built-ins overlaid by agent_profiles.json. id returns one profile.",
			InputSchema: object(nil, props{
				"id":              text("one profile ID"),
				"role":            text("only profiles of this role (orchestrator, implementer, reviewer, refiner, researcher, verifier, qa, custom)"),
				"includeDisabled": flag("include disabled profiles"),
			}),
		},
		{
			Name:        ToolAgentSegments,
			Description: "List prompt segments: base types by goal (report, question, research, development, code_change, ...) and composable tweaks (quick, deep, readonly, ...), built-ins overlaid by prompt_segments.json.",
			InputSchema: object(nil, props{
				"id":   text("one segment ID"),
				"kind": props{"type": "string", "enum": []string{agentcfg.SegmentKindType, agentcfg.SegmentKindTweak}, "description": "only this kind"},
			}),
		},
		{
			Name:        ToolAgentCompose,
			Description: "Resolve a prompt segment plus tags over a raw prompt to a ComposedDispatch: the effective agent profile (harness, model, reasoning), merged instructions, system prompt and final prompt. Unknown tags are warnings.",
			InputSchema: object(nil, props{
				"segment": text("segment ID (default: the catalog default)"),
				"tags":    props{"type": "array", "items": props{"type": "string"}, "description": "tweak or type IDs layered in order"},
				"prompt":  text("the raw prompt"),
			}),
		},
	}
}

// HasTool reports whether name is an agent profile tool.
func (t *AgentProfileTools) HasTool(name string) bool {
	switch name {
	case ToolAgentProfiles, ToolAgentSegments, ToolAgentCompose:
		return true
	}
	return false
}

// Call dispatches one tool call.
func (t *AgentProfileTools) Call(name string, raw json.RawMessage) (any, error) {
	cats, loadErr := agentcfg.LoadAgentCatalogs(t.dir())
	warning := ""
	if loadErr != nil {
		warning = loadErr.Error()
	}
	switch name {
	case ToolAgentProfiles:
		var args AgentProfilesArgs
		if err := decodeArgs(raw, &args); err != nil {
			return nil, err
		}
		return profilesResult(cats.Profiles, args, warning)
	case ToolAgentSegments:
		var args AgentSegmentsArgs
		if err := decodeArgs(raw, &args); err != nil {
			return nil, err
		}
		return segmentsResult(cats.Segments, args, warning)
	case ToolAgentCompose:
		var args AgentComposeArgs
		if err := decodeArgs(raw, &args); err != nil {
			return nil, err
		}
		d, _ := cats.Compose(args.Segment, args.Tags, args.Prompt) // the error is also in d.Warnings
		if warning != "" {
			d.Warnings = append(d.Warnings, warning)
		}
		return d, nil
	}
	return nil, fmt.Errorf("unknown agent profile tool %q", name)
}

func (t *AgentProfileTools) dir() string {
	if t.ConfigDir != "" {
		return t.ConfigDir
	}
	return agentcfg.ConfigDir(t.Getenv)
}

func profilesResult(cat agentcfg.AgentProfileCatalog, args AgentProfilesArgs, warning string) (AgentProfilesResult, error) {
	res := AgentProfilesResult{Default: cat.Default, Roles: agentcfg.Roles, Levels: agentcfg.ReasoningLevels, Warning: warning}
	if args.ID != "" {
		p, ok := cat.Get(args.ID)
		if !ok {
			return res, fmt.Errorf("unknown agent profile %q", args.ID)
		}
		res.Profiles = []agentcfg.AgentProfile{p}
		return res, nil
	}
	for _, p := range cat.List(args.IncludeDisabled) {
		if args.Role == "" || p.Role == args.Role {
			res.Profiles = append(res.Profiles, p)
		}
	}
	return res, nil
}

func segmentsResult(cat agentcfg.PromptSegmentCatalog, args AgentSegmentsArgs, warning string) (AgentSegmentsResult, error) {
	res := AgentSegmentsResult{Default: cat.Default, Warning: warning}
	if args.ID != "" {
		s, ok := cat.Get(args.ID)
		if !ok {
			return res, fmt.Errorf("unknown prompt segment %q", args.ID)
		}
		res.Segments = []agentcfg.PromptSegment{s}
		return res, nil
	}
	res.Segments = cat.List(args.Kind)
	return res, nil
}
