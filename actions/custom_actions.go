package actions

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"ofam.dev/ochazuke/core"
)

// Built-in custom action IDs. Users can override or disable any of these in
// actions.json by matching its ID (for example {"id":"builtin.google_search","disabled":true}).
const (
	BuiltinGoogleSearchID   = "builtin.google_search"
	BuiltinWebOpenMDID      = "builtin.web_open_markdown"
	BuiltinUpgradePreviewID = "builtin.upgrade_preview"
)

// CustomParamSpec declares one typed input parameter for a parameterized
// custom action.
type CustomParamSpec struct {
	Name        string   `json:"name"`
	Label       string   `json:"label,omitempty"`
	Type        string   `json:"type,omitempty"` // string (default), path, enum, bool, int
	Required    bool     `json:"required,omitempty"`
	Default     any      `json:"default,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

// CustomExecSpec defines how a CustomAction executes: an internal Matomeru
// verb, a URL opened in the system browser, a direct argv slice, or a shell
// command template.
type CustomExecSpec struct {
	Verb       string            `json:"verb,omitempty"`
	Args       map[string]any    `json:"args,omitempty"`
	Shell      string            `json:"shell,omitempty"`
	Argv       []string          `json:"argv,omitempty"`
	URL        string            `json:"url,omitempty"`
	Dir        string            `json:"dir,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	Foreground bool              `json:"foreground,omitempty"`
}

// UnmarshalJSON accepts a JSON string (shell template), a JSON string array
// (argv template), or a full CustomExecSpec object.
func (e *CustomExecSpec) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	switch trimmed[0] {
	case '"':
		var shell string
		if err := json.Unmarshal(data, &shell); err != nil {
			return err
		}
		*e = CustomExecSpec{Shell: shell}
		return nil
	case '[':
		var argv []string
		if err := json.Unmarshal(data, &argv); err != nil {
			return err
		}
		*e = CustomExecSpec{Argv: argv}
		return nil
	default:
		type rawExec CustomExecSpec
		var raw rawExec
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}
		*e = CustomExecSpec(raw)
		return nil
	}
}

// IsEmpty reports whether no execution target is configured.
func (e CustomExecSpec) IsEmpty() bool {
	return strings.TrimSpace(e.Verb) == "" &&
		strings.TrimSpace(e.Shell) == "" &&
		strings.TrimSpace(e.URL) == "" &&
		(len(e.Argv) == 0 || strings.TrimSpace(e.Argv[0]) == "")
}

// CustomAction is one declarative parameterized custom action loaded from
// actions.json or seeded from built-in default recipes.
type CustomAction struct {
	ID          string            `json:"id"`
	Title       string            `json:"title,omitempty"`
	Description string            `json:"description,omitempty"`
	Category    string            `json:"category,omitempty"`
	Keywords    []string          `json:"keywords,omitempty"`
	Params      []CustomParamSpec `json:"params,omitempty"`
	Exec        CustomExecSpec    `json:"exec"`
	Disabled    bool              `json:"disabled,omitempty"`
	Builtin     bool              `json:"-"`
	Source      string            `json:"-"`
}

// DisplayTitle returns the human-facing title, falling back to ID.
func (a CustomAction) DisplayTitle() string {
	if t := strings.TrimSpace(a.Title); t != "" {
		return t
	}
	return strings.TrimSpace(a.ID)
}

// DisplayCategory returns the normalized category badge label.
func (a CustomAction) DisplayCategory() string {
	if c := strings.TrimSpace(a.Category); c != "" {
		return strings.ToLower(c)
	}
	return "custom"
}

// HasRequiredParams reports whether the action has at least one required
// parameter without a non-empty default value.
func (a CustomAction) HasRequiredParams() bool {
	for _, p := range a.Params {
		if p.Required && isEmptyCustomDefault(p.Default) {
			return true
		}
	}
	return false
}

func isEmptyCustomDefault(v any) bool {
	if v == nil {
		return true
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s) == ""
	}
	return false
}

// ToParamSpecs converts CustomParamSpecs to ochazuke core.ParamSpecs so the
// shared form widget can render and validate them directly.
func (a CustomAction) ToParamSpecs() []core.ParamSpec {
	if len(a.Params) == 0 {
		return nil
	}
	out := make([]core.ParamSpec, len(a.Params))
	for i, p := range a.Params {
		out[i] = p.ToCoreParamSpec()
	}
	return out
}

// ToCoreParamSpec converts one CustomParamSpec into a core.ParamSpec.
func (p CustomParamSpec) ToCoreParamSpec() core.ParamSpec {
	pt := core.ParamString
	switch strings.ToLower(strings.TrimSpace(p.Type)) {
	case "path":
		pt = core.ParamPath
	case "bool":
		pt = core.ParamBool
	case "int":
		pt = core.ParamInt
	case "enum", "string", "":
		pt = core.ParamString
	}
	desc := customParamDescription(p)
	return core.ParamSpec{
		Name:        strings.TrimSpace(p.Name),
		Type:        pt,
		Required:    p.Required,
		Default:     p.Default,
		Enum:        slices.Clone(p.Enum),
		Description: desc,
	}
}

func customParamDescription(p CustomParamSpec) string {
	var parts []string
	label := strings.TrimSpace(p.Label)
	name := strings.TrimSpace(p.Name)
	if label != "" && !strings.EqualFold(label, name) {
		parts = append(parts, label)
	}
	if d := strings.TrimSpace(p.Description); d != "" {
		parts = append(parts, d)
	}
	if ph := strings.TrimSpace(p.Placeholder); ph != "" {
		parts = append(parts, "e.g. "+ph)
	}
	return strings.Join(parts, " · ")
}

// Validate checks that a CustomAction has a valid ID, valid params, and (when
// not disabled) an execution target.
func (a CustomAction) Validate() error {
	id := strings.TrimSpace(a.ID)
	if id == "" {
		return errors.New("missing id")
	}
	if strings.ContainsAny(id, " \t\n\r") {
		return fmt.Errorf("invalid id %q", id)
	}
	if a.Disabled {
		return nil
	}
	if a.Exec.IsEmpty() {
		return fmt.Errorf("%s: missing exec (set verb, url, shell, or argv)", id)
	}
	return validateCustomParams(id, a.Params)
}

func validateCustomParams(actionID string, params []CustomParamSpec) error {
	seen := make(map[string]bool, len(params))
	for i, p := range params {
		name := strings.TrimSpace(p.Name)
		if name == "" || strings.ContainsAny(name, " \t\n\r") {
			return fmt.Errorf("%s: param %d has invalid name %q", actionID, i+1, p.Name)
		}
		if seen[name] {
			return fmt.Errorf("%s: duplicate param %q", actionID, name)
		}
		seen[name] = true
		kind := strings.ToLower(strings.TrimSpace(p.Type))
		switch kind {
		case "", "string", "path", "bool", "int":
		case "enum":
			if len(p.Enum) == 0 {
				return fmt.Errorf("%s: enum param %q requires choices in enum", actionID, name)
			}
		default:
			return fmt.Errorf("%s: param %q has unknown type %q", actionID, name, p.Type)
		}
	}
	return nil
}

func (a CustomAction) normalized(source string) CustomAction {
	params := make([]CustomParamSpec, len(a.Params))
	for i, p := range a.Params {
		params[i] = CustomParamSpec{
			Name:        strings.TrimSpace(p.Name),
			Label:       strings.TrimSpace(p.Label),
			Type:        strings.ToLower(strings.TrimSpace(p.Type)),
			Required:    p.Required,
			Default:     p.Default,
			Placeholder: strings.TrimSpace(p.Placeholder),
			Description: strings.TrimSpace(p.Description),
			Enum:        slices.Clone(p.Enum),
		}
	}
	return CustomAction{
		ID:          strings.TrimSpace(a.ID),
		Title:       strings.TrimSpace(a.Title),
		Description: strings.TrimSpace(a.Description),
		Category:    strings.ToLower(strings.TrimSpace(a.Category)),
		Keywords:    slices.Clone(a.Keywords),
		Params:      params,
		Exec: CustomExecSpec{
			Verb:       strings.TrimSpace(a.Exec.Verb),
			Args:       maps.Clone(a.Exec.Args),
			Shell:      strings.TrimSpace(a.Exec.Shell),
			Argv:       slices.Clone(a.Exec.Argv),
			URL:        strings.TrimSpace(a.Exec.URL),
			Dir:        strings.TrimSpace(a.Exec.Dir),
			Env:        maps.Clone(a.Exec.Env),
			Foreground: a.Exec.Foreground,
		},
		Disabled: a.Disabled,
		Builtin:  a.Builtin,
		Source:   source,
	}
}

// BuiltinCustomActions returns the declarative default custom action recipes.
// They are pure data and can be disabled globally via ui.builtinCustomActions
// or individually overridden/disabled in actions.json.
func BuiltinCustomActions() []CustomAction {
	return []CustomAction{
		{
			ID:          BuiltinGoogleSearchID,
			Title:       "Google Search",
			Description: "Search Google in your default browser",
			Category:    "web",
			Keywords:    []string{"google", "search", "web", "browser", "lookup"},
			Params: []CustomParamSpec{
				{
					Name:        "query",
					Label:       "Search query",
					Type:        "string",
					Required:    true,
					Placeholder: "golang generics",
				},
			},
			Exec: CustomExecSpec{
				URL: "https://www.google.com/search?q={{query|url}}",
			},
			Builtin: true,
			Source:  "builtin",
		},
		{
			ID:          BuiltinWebOpenMDID,
			Title:       "Open Markdown File in MTM Web Viewer",
			Description: "Pick or enter a .md/.mmd file and open it in the running mdweb viewer",
			Category:    "files",
			Keywords:    []string{"markdown", "md", "mdweb", "web", "viewer", "file", "preview"},
			Params: []CustomParamSpec{
				{
					Name:        "file",
					Label:       "Markdown file",
					Type:        "path",
					Required:    true,
					Placeholder: "docs/README.md (press p to browse)",
				},
			},
			Exec: CustomExecSpec{
				Verb: "web.open",
				Args: map[string]any{"file": "{{file}}"},
			},
			Builtin: true,
			Source:  "builtin",
		},
		{
			ID:          BuiltinUpgradePreviewID,
			Title:       "Preview g U Pending Upgrade",
			Description: "What will g U give me? Compare installed vs running mtm binary & changelog",
			Category:    "system",
			Keywords:    []string{"upgrade", "reload", "version", "changelog", "g U", "binary", "diff"},
			Exec: CustomExecSpec{
				Verb: "upgrade.preview",
			},
			Builtin: true,
			Source:  "builtin",
		},
	}
}

// MergeCustomActions layers user actions from actions.json over built-in
// recipes (when includeBuiltins is true). A user entry with the same ID
// overrides the built-in recipe; setting disabled=true removes it.
func MergeCustomActions(includeBuiltins bool, user []CustomAction) []CustomAction {
	byID := make(map[string]CustomAction, len(user)+3)
	order := make([]string, 0, len(user)+3)
	add := func(ca CustomAction) {
		if _, exists := byID[ca.ID]; !exists {
			order = append(order, ca.ID)
		}
		byID[ca.ID] = ca
	}
	if includeBuiltins {
		for _, b := range BuiltinCustomActions() {
			add(b)
		}
	}
	for _, u := range user {
		add(u)
	}
	out := make([]CustomAction, 0, len(order))
	for _, id := range order {
		ca := byID[id]
		if ca.Disabled {
			continue
		}
		out = append(out, ca)
	}
	return out
}

// LoadAllFile reads actions.json and returns both legacy record-scoped
// ActionDefs and declarative parameterized CustomActions. A missing file
// returns (nil, nil, nil).
func LoadAllFile(path string) ([]ActionDef, []CustomAction, error) {
	if strings.TrimSpace(path) == "" {
		path = DefaultConfigPath()
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	return ParseAll(raw)
}

// ParseAll parses a JSON or JSONC actions.json payload into legacy ActionDefs
// and parameterized CustomActions. Supports:
//   - Top-level array [...] mixing legacy ActionDef and CustomAction entries
//   - Object {"actions": [...], "customActions": [...]}
func ParseAll(raw []byte) ([]ActionDef, []CustomAction, error) {
	clean := stripJSONC(raw)
	trimmed := strings.TrimSpace(string(clean))
	if trimmed == "" {
		return nil, nil, nil
	}

	mixedRaw, explicitCustomRaw, err := splitRawActionsPayload(clean, trimmed[0])
	if err != nil {
		return nil, nil, err
	}

	var defs []ActionDef
	var customs []CustomAction
	seenDefs := map[string]int{}
	seenCustoms := map[string]int{}

	for i, elem := range mixedRaw {
		if isCustomActionEntry(elem) {
			ca, err := parseCustomActionEntry(elem, i+1)
			if err != nil {
				return nil, nil, err
			}
			if prev, dup := seenCustoms[ca.ID]; dup {
				return nil, nil, fmt.Errorf("actions: entry %d duplicates custom action %q (first defined at entry %d)", i+1, ca.ID, prev)
			}
			seenCustoms[ca.ID] = i + 1
			customs = append(customs, ca)
			continue
		}
		def, err := parseLegacyActionEntry(elem, i+1)
		if err != nil {
			return nil, nil, err
		}
		name := def.CommandName()
		if prev, dup := seenDefs[name]; dup {
			return nil, nil, fmt.Errorf("actions: entry %d duplicates command %q (first defined at entry %d)", i+1, name, prev)
		}
		seenDefs[name] = i + 1
		defs = append(defs, def)
	}

	for i, elem := range explicitCustomRaw {
		ca, err := parseCustomActionEntry(elem, i+1)
		if err != nil {
			return nil, nil, err
		}
		if prev, dup := seenCustoms[ca.ID]; dup {
			return nil, nil, fmt.Errorf("actions: customActions entry %d duplicates custom action %q (first defined at entry %d)", i+1, ca.ID, prev)
		}
		seenCustoms[ca.ID] = i + 1
		customs = append(customs, ca)
	}
	return defs, customs, nil
}

func splitRawActionsPayload(clean []byte, first byte) ([]json.RawMessage, []json.RawMessage, error) {
	switch first {
	case '[':
		var list []json.RawMessage
		if err := json.Unmarshal(clean, &list); err != nil {
			return nil, nil, fmt.Errorf("actions: parse array: %w", err)
		}
		return list, nil, nil
	case '{':
		var wrap struct {
			Actions       []json.RawMessage `json:"actions"`
			CustomActions []json.RawMessage `json:"customActions"`
		}
		if err := json.Unmarshal(clean, &wrap); err != nil {
			return nil, nil, fmt.Errorf("actions: parse object: %w", err)
		}
		if wrap.Actions == nil && wrap.CustomActions == nil {
			return nil, nil, errors.New("actions: object form requires an \"actions\" or \"customActions\" array")
		}
		return wrap.Actions, wrap.CustomActions, nil
	default:
		return nil, nil, errors.New("actions: config must be a JSON array or {\"actions\": [...]}")
	}
}

func isCustomActionEntry(raw json.RawMessage) bool {
	var peek struct {
		ID   string `json:"id"`
		Noun string `json:"noun"`
		Verb string `json:"verb"`
	}
	if err := json.Unmarshal(raw, &peek); err != nil {
		return false
	}
	return strings.TrimSpace(peek.ID) != "" && strings.TrimSpace(peek.Noun) == "" && strings.TrimSpace(peek.Verb) == ""
}

func parseLegacyActionEntry(raw json.RawMessage, idx int) (ActionDef, error) {
	var def ActionDef
	if err := json.Unmarshal(raw, &def); err != nil {
		return ActionDef{}, fmt.Errorf("actions: entry %d: %w", idx, err)
	}
	norm := def.normalized()
	if err := norm.Validate(); err != nil {
		return ActionDef{}, fmt.Errorf("actions: entry %d: %w", idx, err)
	}
	return norm, nil
}

func parseCustomActionEntry(raw json.RawMessage, idx int) (CustomAction, error) {
	var ca CustomAction
	if err := json.Unmarshal(raw, &ca); err != nil {
		return CustomAction{}, fmt.Errorf("actions: entry %d: %w", idx, err)
	}
	norm := ca.normalized(ActionsFile)
	if err := norm.Validate(); err != nil {
		return CustomAction{}, fmt.Errorf("actions: entry %d: %w", idx, err)
	}
	return norm, nil
}

// ExpandArgs expands string values inside an args map using ExpandTemplate
// without mutating the source map.
func ExpandArgs(args map[string]any, vars map[string]string) map[string]any {
	if len(args) == 0 {
		return nil
	}
	out := make(map[string]any, len(args))
	for k, v := range args {
		if s, ok := v.(string); ok {
			out[k] = ExpandTemplate(s, vars)
		} else {
			out[k] = v
		}
	}
	return out
}
