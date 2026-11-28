// Package config holds Matomeru's pure-data agent dispatch catalogs: standard
// agent profiles (agent_profiles.json) and prompt segments
// (prompt_segments.json). Built-in defaults ship in code; the user's files in
// the config directory overlay them field by field.
//
// Everything here is data plus pure functions over it. Nothing runs a
// process, touches the store or draws UI, so pane spawn, queues, the refiner
// and subagent role pickers all share one vocabulary.
//
// config imports only the standard library.
package config

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// File names inside the config directory.
const (
	AgentProfilesFileName  = "agent_profiles.json"
	PromptSegmentsFileName = "prompt_segments.json"
)

// Standard agent roles. Role is open vocabulary: any other string is kept as
// is and reads as a custom role.
const (
	RoleOrchestrator = "orchestrator"
	RoleImplementer  = "implementer"
	RoleReviewer     = "reviewer"
	RoleRefiner      = "refiner"
	RoleResearcher   = "researcher"
	RoleVerifier     = "verifier"
	RoleQA           = "qa"
	RoleCustom       = "custom"
)

// Roles lists the standard roles in display order.
var Roles = []string{RoleOrchestrator, RoleImplementer, RoleReviewer, RoleRefiner, RoleResearcher, RoleVerifier, RoleQA, RoleCustom}

// Standard reasoning levels. Like Role, the field is open vocabulary.
const (
	ReasoningLow     = "low"
	ReasoningMedium  = "medium"
	ReasoningHigh    = "high"
	ReasoningXHigh   = "xhigh"
	ReasoningInherit = "inherit"
)

// ReasoningLevels lists the standard reasoning levels, lowest first.
var ReasoningLevels = []string{ReasoningLow, ReasoningMedium, ReasoningHigh, ReasoningXHigh, ReasoningInherit}

// AgentProfile is one reusable, standard set of agent settings: which harness
// and model run the agent, how hard it thinks, and what it is told.
type AgentProfile struct {
	ID             string   `json:"id"`
	Name           string   `json:"name,omitempty"`
	Role           string   `json:"role,omitempty"`
	Harness        string   `json:"harness,omitempty"`
	Provider       string   `json:"provider,omitempty"`
	Model          string   `json:"model,omitempty"`
	ReasoningLevel string   `json:"reasoningLevel,omitempty"`
	SystemPrompt   string   `json:"systemPrompt,omitempty"`
	PromptPrefix   string   `json:"promptPrefix,omitempty"`
	PromptSuffix   string   `json:"promptSuffix,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	DefaultCwd     string   `json:"defaultCwd,omitempty"`
	Notes          string   `json:"notes,omitempty"`
	Disabled       bool     `json:"disabled,omitempty"`
	BuiltIn        bool     `json:"builtIn,omitempty"`
}

// AgentProfileFile is the on-disk shape of agent_profiles.json. Profiles is
// keyed by ID; a key that names a built-in overlays it, any other key adds a
// profile.
type AgentProfileFile struct {
	Default  string                  `json:"default,omitempty"`
	Profiles map[string]AgentProfile `json:"profiles,omitempty"`
}

// AgentProfileCatalog is the merged view: built-ins overlaid by the user file.
type AgentProfileCatalog struct {
	Default  string                  `json:"default,omitempty"`
	Profiles map[string]AgentProfile `json:"profiles"`
}

// DefaultAgentProfileID is the profile used when nothing else names one.
const DefaultAgentProfileID = "opus-implementer"

// DefaultAgentProfiles returns the built-in profiles of the OFAM
// multi-provider fleet. Every field can be overridden in agent_profiles.json.
func DefaultAgentProfiles() map[string]AgentProfile {
	list := []AgentProfile{
		{
			ID: "neon-architect", Name: "Neon architect", Role: RoleOrchestrator,
			Harness: "antigravity", Provider: "antigravity", ReasoningLevel: ReasoningHigh,
			SystemPrompt: "You orchestrate: own architecture, interface and API design, owner alignment and synthesis of delegated reports. Delegate implementation, research and refactors to worker profiles.",
			Tags:         []string{"neon", "architecture", "orchestration"},
			Notes:        "Neon quota is precious: architecture, interface design, pre-PR review and report synthesis only.",
		},
		{
			ID: "fable-implementer", Name: "Fable 5.2 implementer", Role: RoleImplementer,
			Harness: "claude-fable", Provider: "anthropic", Model: "claude-fable-5-2", ReasoningLevel: ReasoningHigh,
			SystemPrompt: "You implement one lane in its own git worktree and branch. Commit on the lane branch; never merge, tag or open a PR.",
			Tags:         []string{"implementation", "hard"},
			Notes:        "For the hardest cross-cutting changes, or ones Opus could not get right.",
		},
		{
			ID: "opus-implementer", Name: "Opus 5.5 implementer", Role: RoleImplementer,
			Harness: "claude-opus", Provider: "anthropic", Model: "claude-opus-5-5", ReasoningLevel: ReasoningInherit,
			SystemPrompt: "You implement one lane in its own git worktree and branch. Commit on the lane branch; never merge, tag or open a PR.",
			Tags:         []string{"implementation", "research", "refactor"},
			Notes:        "Primary worker: features across several files, library mechanisms, research and triage.",
		},
		{
			ID: "sol-worker", Name: "Sol worker", Role: RoleImplementer,
			Harness: "codex-sol", Provider: "openai", Model: "gpt-6.1-sol", ReasoningLevel: ReasoningHigh,
			SystemPrompt: "You implement one bounded lane in its own worktree and branch and report back through the mailbox.",
			Tags:         []string{"implementation", "parallel", "overflow"},
			Notes:        "Secondary worker; move lanes here once the Claude weekly usage passes about 60%.",
		},
		{
			ID: "astra-worker", Name: "Astra worker", Role: RoleVerifier,
			Harness: "codex-astra", Provider: "openai", ReasoningLevel: ReasoningMedium,
			SystemPrompt: "You verify: reproduce, run the targeted checks, and report pass or fail with evidence. Do not widen scope.",
			Tags:         []string{"verification", "mockup", "parallel"},
			Notes:        "Parallel verification and mockup worker.",
		},
		{
			ID: "refiner-default", Name: "Prompt refiner", Role: RoleRefiner,
			Harness: "claude-opus", Provider: "anthropic", Model: "claude-opus-5-5", ReasoningLevel: ReasoningMedium,
			SystemPrompt: "You refine a raw owner request into a self-contained brief: goal, files, rules, what to reuse, what done means and the checks to run. Do not implement it.",
			Tags:         []string{"refine", "brief"},
		},
		{
			ID: "reviewer-neon", Name: "Neon pre-PR reviewer", Role: RoleReviewer,
			Harness: "antigravity", Provider: "antigravity", ReasoningLevel: ReasoningHigh,
			SystemPrompt: "You review a pushed lane branch before any PR is opened. Push back on special cases, weak code and rule violations; approve or reject with a punch list.",
			Tags:         []string{"neon", "review", "pre-pr"},
			Notes:        "The hard pre-PR gate: no gh pr create until this profile approves.",
		},
		{
			ID: "haiku-scanner", Name: "Haiku scanner", Role: RoleResearcher,
			Harness: "claude", Provider: "anthropic", Model: "claude-haiku-5-5", ReasoningLevel: ReasoningLow,
			SystemPrompt: "You run large, repetitive read-only investigations (scans, inventories, per-item audits) and return a compact table. Never edit code.",
			Tags:         []string{"scan", "inventory", "readonly"},
			Notes:        "Never for code changes.",
		},
	}
	out := make(map[string]AgentProfile, len(list))
	for _, p := range list {
		p.BuiltIn = true
		out[p.ID] = p
	}
	return out
}

// DefaultAgentProfileCatalog returns the built-in catalog with no overlay.
func DefaultAgentProfileCatalog() AgentProfileCatalog {
	return AgentProfileCatalog{Default: DefaultAgentProfileID, Profiles: DefaultAgentProfiles()}
}

// MergeAgentProfiles overlays file onto the built-ins. A file entry that
// names a built-in replaces only the fields it sets (Tags replace as a whole
// when present); other entries are added as user profiles.
func MergeAgentProfiles(file AgentProfileFile) AgentProfileCatalog {
	cat := DefaultAgentProfileCatalog()
	if file.Default != "" {
		cat.Default = file.Default
	}
	for key, over := range file.Profiles {
		id := cmp.Or(strings.TrimSpace(over.ID), strings.TrimSpace(key))
		if id == "" {
			continue
		}
		over.ID = id
		base, builtIn := cat.Profiles[id]
		merged := overlayProfile(base, over)
		merged.BuiltIn = builtIn
		cat.Profiles[id] = merged
	}
	return cat
}

func overlayProfile(base, over AgentProfile) AgentProfile {
	out := base
	out.ID = over.ID
	for _, f := range []struct {
		dst *string
		src string
	}{
		{&out.Name, over.Name}, {&out.Role, over.Role}, {&out.Harness, over.Harness},
		{&out.Provider, over.Provider}, {&out.Model, over.Model}, {&out.ReasoningLevel, over.ReasoningLevel},
		{&out.SystemPrompt, over.SystemPrompt}, {&out.PromptPrefix, over.PromptPrefix},
		{&out.PromptSuffix, over.PromptSuffix}, {&out.DefaultCwd, over.DefaultCwd}, {&out.Notes, over.Notes},
	} {
		if f.src != "" {
			*f.dst = f.src
		}
	}
	if over.Tags != nil {
		out.Tags = slices.Clone(over.Tags)
	}
	out.Disabled = over.Disabled
	return out
}

// Get returns the profile with id (enabled or not).
func (c AgentProfileCatalog) Get(id string) (AgentProfile, bool) {
	p, ok := c.Profiles[strings.TrimSpace(id)]
	return p, ok
}

// List returns every enabled profile sorted by role order then ID. Pass
// includeDisabled to see disabled ones too.
func (c AgentProfileCatalog) List(includeDisabled bool) []AgentProfile {
	out := make([]AgentProfile, 0, len(c.Profiles))
	for _, p := range c.Profiles {
		if p.Disabled && !includeDisabled {
			continue
		}
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b AgentProfile) int {
		return cmp.Or(cmp.Compare(roleRank(a.Role), roleRank(b.Role)), cmp.Compare(a.ID, b.ID))
	})
	return out
}

// ByRole returns the enabled profiles whose Role is role.
func (c AgentProfileCatalog) ByRole(role string) []AgentProfile {
	return slices.DeleteFunc(c.List(false), func(p AgentProfile) bool { return p.Role != role })
}

// IDs returns every profile ID, sorted.
func (c AgentProfileCatalog) IDs() []string {
	return slices.Sorted(maps.Keys(c.Profiles))
}

// DefaultProfile returns the catalog's default profile, falling back to the
// built-in default and then to any enabled profile.
func (c AgentProfileCatalog) DefaultProfile() (AgentProfile, bool) {
	for _, id := range []string{c.Default, DefaultAgentProfileID} {
		if p, ok := c.Get(id); ok && !p.Disabled {
			return p, true
		}
	}
	if list := c.List(false); len(list) > 0 {
		return list[0], true
	}
	return AgentProfile{}, false
}

func roleRank(role string) int {
	if i := slices.Index(Roles, role); i >= 0 {
		return i
	}
	return len(Roles)
}

// AgentProfilesPath returns dir/agent_profiles.json.
func AgentProfilesPath(dir string) string {
	return filepath.Join(dir, AgentProfilesFileName)
}

// LoadAgentProfileFile reads agent_profiles.json. A missing file is an empty
// overlay, not an error.
func LoadAgentProfileFile(path string) (AgentProfileFile, error) {
	var file AgentProfileFile
	err := readJSON(path, &file)
	return file, err
}

// LoadAgentProfiles reads path and returns the merged catalog. On a parse
// error the built-in catalog is still returned with the error.
func LoadAgentProfiles(path string) (AgentProfileCatalog, error) {
	file, err := LoadAgentProfileFile(path)
	if err != nil {
		return DefaultAgentProfileCatalog(), err
	}
	return MergeAgentProfiles(file), nil
}

// SaveAgentProfileFile writes the user overlay atomically. BuiltIn is a
// derived field and is never written.
func SaveAgentProfileFile(path string, file AgentProfileFile) error {
	clean := AgentProfileFile{Default: file.Default, Profiles: make(map[string]AgentProfile, len(file.Profiles))}
	for k, p := range file.Profiles {
		p.BuiltIn = false
		clean.Profiles[k] = p
	}
	return writeJSON(path, clean)
}

// ConfigDir returns the Matomeru config directory: $MTM_CONFIG_DIR, else
// $XDG_CONFIG_HOME/matomeru, else ~/.config/matomeru. getenv nil is
// os.Getenv.
func ConfigDir(getenv func(string) string) string {
	if getenv == nil {
		getenv = os.Getenv
	}
	if dir := strings.TrimSpace(getenv("MTM_CONFIG_DIR")); dir != "" {
		return dir
	}
	if xdg := strings.TrimSpace(getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, "matomeru")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".config", "matomeru")
	}
	return filepath.Join(home, ".config", "matomeru")
}

func readJSON(path string, v any) error {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return nil
}

func writeJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
