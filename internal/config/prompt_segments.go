package config

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
)

// Segment kinds. A type is the base classification of a prompt by its goal;
// a tweak is a composable modifier layered on top of a type.
const (
	SegmentKindType  = "type"
	SegmentKindTweak = "tweak"
)

// Template placeholders understood by ComposePrompt.
const (
	PlaceholderPrompt       = "{{prompt}}"
	PlaceholderInstructions = "{{instructions}}"
	PlaceholderSegment      = "{{segment}}"
	PlaceholderTags         = "{{tags}}"
	PlaceholderBudget       = "{{budget}}"
)

// ErrUnknownSegment is returned (wrapped) when a segment ID is not in the
// catalog. ComposePrompt still returns a usable dispatch alongside it.
var ErrUnknownSegment = errors.New("unknown prompt segment")

// PromptSegment classifies a prompt (Kind type) or tweaks one (Kind tweak).
// ProfileID picks the agent profile; the optional Harness, Model and
// ReasoningLevel fields override single fields of that profile.
type PromptSegment struct {
	ID             string   `json:"id"`
	Name           string   `json:"name,omitempty"`
	Kind           string   `json:"kind,omitempty"`
	Goal           string   `json:"goal,omitempty"`
	ProfileID      string   `json:"profileId,omitempty"`
	Harness        string   `json:"harness,omitempty"`
	Model          string   `json:"model,omitempty"`
	ReasoningLevel string   `json:"reasoningLevel,omitempty"`
	Instructions   string   `json:"instructions,omitempty"`
	PromptTemplate string   `json:"promptTemplate,omitempty"`
	BudgetHint     string   `json:"budgetHint,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	Disabled       bool     `json:"disabled,omitempty"`
	BuiltIn        bool     `json:"builtIn,omitempty"`
}

// IsTweak reports whether s is a tweak modifier rather than a base type.
func (s PromptSegment) IsTweak() bool { return s.Kind == SegmentKindTweak }

// PromptSegmentFile is the on-disk shape of prompt_segments.json.
type PromptSegmentFile struct {
	Default  string                   `json:"default,omitempty"`
	Segments map[string]PromptSegment `json:"segments,omitempty"`
}

// PromptSegmentCatalog is the merged view: built-ins overlaid by the user
// file.
type PromptSegmentCatalog struct {
	Default  string                   `json:"default,omitempty"`
	Segments map[string]PromptSegment `json:"segments"`
}

// DefaultPromptSegmentID is the type used when a caller names none.
const DefaultPromptSegmentID = "question"

// ComposedDispatch is the resolved result of a segment, its tweaks and a raw
// prompt: the effective profile, the merged instructions and the final
// prompt a harness should receive.
type ComposedDispatch struct {
	SegmentID     string       `json:"segmentId"`
	Tags          []string     `json:"tags,omitempty"`
	Profile       AgentProfile `json:"profile"`
	ProfileSource string       `json:"profileSource,omitempty"`
	SystemPrompt  string       `json:"systemPrompt,omitempty"`
	Instructions  string       `json:"instructions,omitempty"`
	Prompt        string       `json:"prompt"`
	BudgetHint    string       `json:"budgetHint,omitempty"`
	Warnings      []string     `json:"warnings,omitempty"`
}

func typeSeg(id, name, goal, profile, reasoning, budget, instructions string) PromptSegment {
	return PromptSegment{ID: id, Name: name, Kind: SegmentKindType, Goal: goal, ProfileID: profile,
		ReasoningLevel: reasoning, BudgetHint: budget, Instructions: instructions}
}

func tweakSeg(id, name, goal, profile, reasoning, budget, instructions string) PromptSegment {
	return PromptSegment{ID: id, Name: name, Kind: SegmentKindTweak, Goal: goal, ProfileID: profile,
		ReasoningLevel: reasoning, BudgetHint: budget, Instructions: instructions}
}

// DefaultPromptSegments returns the built-in types and tweaks.
func DefaultPromptSegments() map[string]PromptSegment {
	list := []PromptSegment{
		typeSeg("report", "Report", "summarize work or state for the owner", "opus-implementer", ReasoningMedium, "small",
			"Write a concise structured Markdown report: headings, bullets, bold labels. Lead with the outcome, then evidence (SHAs, test results, links)."),
		typeSeg("question", "Question", "answer an owner question", "opus-implementer", "", "small",
			"Answer the question directly first, then the supporting detail. Record the Q&A through mtm."),
		typeSeg("small_question", "Small question", "a quick factual answer", "haiku-scanner", ReasoningLow, "tiny",
			"Answer in a few lines. Do not explore beyond what the answer needs."),
		typeSeg("medium_question", "Medium question", "an answer that needs some reading", "opus-implementer", ReasoningMedium, "small",
			"Read only what the answer needs, cite file:line evidence, answer in under a page."),
		typeSeg("large_question", "Large question", "an answer that needs investigation or design judgment", "opus-implementer", ReasoningXHigh, "large",
			"Investigate thoroughly, weigh the options, give a recommendation, and publish anything over 10 lines as its own page."),
		typeSeg("queue_orchestration", "Queue orchestration", "plan, route and track queued work", "neon-architect", ReasoningHigh, "medium",
			"Triage the queue into lanes, pick a profile per lane, write self-contained briefs, and keep lane statuses and dossiers current. Delegate; do not implement."),
		typeSeg("document", "Document", "draft or revise a Markdown document", "opus-implementer", ReasoningLow, "medium",
			"Write the document as structured Markdown and link it to the lane as its dossier."),
		typeSeg("page", "Page", "publish a ui.page for the owner", "opus-implementer", ReasoningMedium, "medium",
			"Publish the result as a ui.page (kind markdown for rich text) and give the owner its slug."),
		typeSeg("research", "Research", "investigate code or the web and synthesize findings", "opus-implementer", ReasoningHigh, "large",
			"Research read-only, collect evidence with sources or file:line references, and write findings to a linked document."),
		typeSeg("development", "Development", "build a feature across several files", "fable-implementer", ReasoningHigh, "large",
			"Work in your own worktree and branch, reuse before writing, keep data separate from behavior, run the targeted checks, commit, and report for review."),
		typeSeg("code_change", "Code change", "a bounded fix or change", "opus-implementer", ReasoningMedium, "medium",
			"Make the smallest correct change in your own worktree and branch, cover it with one test at the right layer, commit, and report for review."),
		typeSeg("tooling", "Tooling", "scripts, CLIs, configuration and developer tooling", "sol-worker", ReasoningMedium, "medium",
			"Prefer configuration over code; when code is needed, keep it a small reusable tool and record any owner command in o-cmds."),
		tweakSeg("quick", "Quick", "trade depth for speed", "", ReasoningLow, "tiny",
			"Be brief: the shortest correct answer or change."),
		tweakSeg("deep", "Deep", "think harder", "", ReasoningXHigh, "large",
			"Take the time to consider alternatives and edge cases before acting."),
		tweakSeg("readonly", "Read-only", "never edit files", "", "", "",
			"Read only: do not edit, commit or push anything."),
		tweakSeg("with_tests", "With tests", "require tests", "", "", "",
			"Cover each new behaviour with one test at the layer where it happens, and run the targeted tests."),
		tweakSeg("neon_review", "Neon review", "route to the pre-PR reviewer", "reviewer-neon", "", "",
			"Review the pushed branch against the repository rules; approve or reject with a punch list."),
		tweakSeg("scan", "Scan", "high-volume mechanical investigation", "haiku-scanner", ReasoningLow, "",
			"Treat this as a mechanical scan: use deterministic programs, return a compact table."),
		tweakSeg("overflow", "Overflow", "run outside the Claude quota", "sol-worker", "", "",
			"You are running as overflow capacity: stay inside the brief and report back through the mailbox."),
		tweakSeg("publish_page", "Publish page", "end with a ui.page", "", "", "",
			"Publish the final result as a ui.page and include its slug in your report."),
	}
	out := make(map[string]PromptSegment, len(list))
	for _, s := range list {
		s.BuiltIn = true
		out[s.ID] = s
	}
	return out
}

// DefaultPromptSegmentCatalog returns the built-in catalog with no overlay.
func DefaultPromptSegmentCatalog() PromptSegmentCatalog {
	return PromptSegmentCatalog{Default: DefaultPromptSegmentID, Segments: DefaultPromptSegments()}
}

// MergePromptSegments overlays file onto the built-ins field by field, as
// MergeAgentProfiles does for profiles.
func MergePromptSegments(file PromptSegmentFile) PromptSegmentCatalog {
	cat := DefaultPromptSegmentCatalog()
	if file.Default != "" {
		cat.Default = file.Default
	}
	for key, over := range file.Segments {
		id := cmp.Or(strings.TrimSpace(over.ID), strings.TrimSpace(key))
		if id == "" {
			continue
		}
		over.ID = id
		base, builtIn := cat.Segments[id]
		merged := overlaySegment(base, over)
		merged.BuiltIn = builtIn
		if merged.Kind == "" {
			merged.Kind = SegmentKindType
		}
		cat.Segments[id] = merged
	}
	return cat
}

func overlaySegment(base, over PromptSegment) PromptSegment {
	out := base
	out.ID = over.ID
	for _, f := range []struct {
		dst *string
		src string
	}{
		{&out.Name, over.Name}, {&out.Kind, over.Kind}, {&out.Goal, over.Goal},
		{&out.ProfileID, over.ProfileID}, {&out.Harness, over.Harness}, {&out.Model, over.Model},
		{&out.ReasoningLevel, over.ReasoningLevel}, {&out.Instructions, over.Instructions},
		{&out.PromptTemplate, over.PromptTemplate}, {&out.BudgetHint, over.BudgetHint},
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

// Get returns the segment with id.
func (c PromptSegmentCatalog) Get(id string) (PromptSegment, bool) {
	s, ok := c.Segments[strings.TrimSpace(id)]
	return s, ok
}

// List returns the enabled segments of kind ("" is every kind): types first,
// then tweaks, each sorted by ID.
func (c PromptSegmentCatalog) List(kind string) []PromptSegment {
	out := make([]PromptSegment, 0, len(c.Segments))
	for _, s := range c.Segments {
		if s.Disabled || (kind != "" && s.Kind != kind) {
			continue
		}
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b PromptSegment) int {
		return cmp.Or(cmp.Compare(kindRank(a.Kind), kindRank(b.Kind)), cmp.Compare(a.ID, b.ID))
	})
	return out
}

// IDs returns every segment ID, sorted.
func (c PromptSegmentCatalog) IDs() []string {
	return slices.Sorted(maps.Keys(c.Segments))
}

func kindRank(kind string) int {
	if kind == SegmentKindTweak {
		return 1
	}
	return 0
}

// PromptSegmentsPath returns dir/prompt_segments.json.
func PromptSegmentsPath(dir string) string {
	return filepath.Join(dir, PromptSegmentsFileName)
}

// LoadPromptSegmentFile reads prompt_segments.json; a missing file is empty.
func LoadPromptSegmentFile(path string) (PromptSegmentFile, error) {
	var file PromptSegmentFile
	err := readJSON(path, &file)
	return file, err
}

// LoadPromptSegments reads path and returns the merged catalog. On a parse
// error the built-in catalog is still returned with the error.
func LoadPromptSegments(path string) (PromptSegmentCatalog, error) {
	file, err := LoadPromptSegmentFile(path)
	if err != nil {
		return DefaultPromptSegmentCatalog(), err
	}
	return MergePromptSegments(file), nil
}

// SavePromptSegmentFile writes the user overlay atomically.
func SavePromptSegmentFile(path string, file PromptSegmentFile) error {
	clean := PromptSegmentFile{Default: file.Default, Segments: make(map[string]PromptSegment, len(file.Segments))}
	for k, s := range file.Segments {
		s.BuiltIn = false
		clean.Segments[k] = s
	}
	return writeJSON(path, clean)
}

// ComposePrompt resolves segmentID plus tagIDs over rawPrompt.
//
// Layers apply in order: the segment, then each tag in the order given (a tag
// may be a tweak or another type). The last layer naming a ProfileID picks
// the profile; then, layer by layer, Harness, Model and ReasoningLevel
// override single fields of it (so "deep" still deepens a profile a later
// tag picked), BudgetHint replaces the hint and Instructions append. Tags the segment
// lists itself apply before the caller's tags. The final prompt is the
// profile's PromptPrefix, the merged instructions, the segment's template
// over the raw prompt, and the profile's PromptSuffix, joined by blank lines.
//
// Unknown tags or profiles are warnings, never errors. An unknown or empty
// segment falls back to the catalog default and returns a dispatch together
// with an error wrapping ErrUnknownSegment, so a caller can still proceed.
func ComposePrompt(segmentID string, tagIDs []string, rawPrompt string, cfg PromptSegmentCatalog, profiles AgentProfileCatalog) (ComposedDispatch, error) {
	var d ComposedDispatch
	var err error
	seg, ok := cfg.Get(cmp.Or(strings.TrimSpace(segmentID), cfg.Default))
	if !ok {
		err = fmt.Errorf("%w %q", ErrUnknownSegment, segmentID)
		d.Warnings = append(d.Warnings, err.Error())
		seg, _ = cfg.Get(cmp.Or(cfg.Default, DefaultPromptSegmentID))
	}
	d.SegmentID = seg.ID
	if p, ok := profiles.DefaultProfile(); ok {
		d.Profile, d.ProfileSource = p, "default"
	}
	var instructions []string
	layers := append([]PromptSegment{seg}, resolveTags(append(slices.Clone(seg.Tags), tagIDs...), cfg, &d)...)
	for _, layer := range layers {
		d.selectProfile(layer, profiles)
	}
	for _, layer := range layers {
		d.applyOverrides(layer)
		if s := strings.TrimSpace(layer.Instructions); s != "" {
			instructions = append(instructions, s)
		}
	}
	d.Instructions = strings.Join(instructions, "\n\n")
	d.SystemPrompt = d.Profile.SystemPrompt
	body := renderTemplate(seg.PromptTemplate, rawPrompt, d)
	lead := d.Instructions
	if strings.Contains(seg.PromptTemplate, PlaceholderInstructions) {
		lead = "" // the template places the instructions itself
	}
	d.Prompt = joinNonEmpty(d.Profile.PromptPrefix, lead, body, d.Profile.PromptSuffix)
	return d, err
}

func resolveTags(ids []string, cfg PromptSegmentCatalog, d *ComposedDispatch) []PromptSegment {
	var out []PromptSegment
	seen := map[string]bool{d.SegmentID: true}
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		tag, ok := cfg.Get(id)
		if !ok || tag.Disabled {
			d.Warnings = append(d.Warnings, fmt.Sprintf("unknown prompt tag %q ignored", id))
			continue
		}
		d.Tags = append(d.Tags, id)
		out = append(out, tag)
	}
	return out
}

func (d *ComposedDispatch) selectProfile(layer PromptSegment, profiles AgentProfileCatalog) {
	id := strings.TrimSpace(layer.ProfileID)
	if id == "" {
		return
	}
	if p, ok := profiles.Get(id); ok && !p.Disabled {
		d.Profile, d.ProfileSource = p, layer.ID
		return
	}
	d.Warnings = append(d.Warnings, fmt.Sprintf("%s: unknown agent profile %q kept %q", layer.ID, id, d.Profile.ID))
}

func (d *ComposedDispatch) applyOverrides(layer PromptSegment) {
	d.Profile.Harness = cmp.Or(layer.Harness, d.Profile.Harness)
	d.Profile.Model = cmp.Or(layer.Model, d.Profile.Model)
	d.Profile.ReasoningLevel = cmp.Or(layer.ReasoningLevel, d.Profile.ReasoningLevel)
	d.BudgetHint = cmp.Or(layer.BudgetHint, d.BudgetHint)
}

func renderTemplate(tmpl, rawPrompt string, d ComposedDispatch) string {
	if strings.TrimSpace(tmpl) == "" {
		return strings.TrimSpace(rawPrompt)
	}
	r := strings.NewReplacer(
		PlaceholderPrompt, strings.TrimSpace(rawPrompt),
		PlaceholderInstructions, d.Instructions,
		PlaceholderSegment, d.SegmentID,
		PlaceholderTags, strings.Join(d.Tags, ", "),
		PlaceholderBudget, d.BudgetHint,
	)
	return strings.TrimSpace(r.Replace(tmpl))
}

func joinNonEmpty(parts ...string) string {
	return strings.Join(slices.DeleteFunc(slices.Clone(parts), func(s string) bool { return strings.TrimSpace(s) == "" }), "\n\n")
}

// AgentCatalogs bundles the two merged catalogs a dispatcher needs.
type AgentCatalogs struct {
	Profiles AgentProfileCatalog  `json:"profiles"`
	Segments PromptSegmentCatalog `json:"segments"`
}

// LoadAgentCatalogs loads agent_profiles.json and prompt_segments.json from
// dir. A file that fails to parse leaves its built-ins in place and its error
// joined into the returned error; an empty dir yields the built-ins.
func LoadAgentCatalogs(dir string) (AgentCatalogs, error) {
	if strings.TrimSpace(dir) == "" {
		return AgentCatalogs{Profiles: DefaultAgentProfileCatalog(), Segments: DefaultPromptSegmentCatalog()}, nil
	}
	profiles, perr := LoadAgentProfiles(AgentProfilesPath(dir))
	segments, serr := LoadPromptSegments(PromptSegmentsPath(dir))
	return AgentCatalogs{Profiles: profiles, Segments: segments}, errors.Join(perr, serr)
}

// Compose is ComposePrompt over the bundled catalogs.
func (c AgentCatalogs) Compose(segmentID string, tagIDs []string, rawPrompt string) (ComposedDispatch, error) {
	return ComposePrompt(segmentID, tagIDs, rawPrompt, c.Segments, c.Profiles)
}
