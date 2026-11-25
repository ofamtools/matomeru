package mdweb

// SourceSpan holds exact UTF-8 byte offsets [Start, End) into the raw Markdown
// source string, along with 1-based start and end line numbers.
type SourceSpan struct {
	Start   int `json:"start"`
	End     int `json:"end"`
	Line    int `json:"line"`
	EndLine int `json:"endLine"`
}

// HeadingInfo describes a heading for the table of contents.
type HeadingInfo struct {
	ID    string `json:"id"`
	Level int    `json:"level"`
	Text  string `json:"text"`
	Line  int    `json:"line"`
}

// WidgetKind identifies an interactive control embedded in Markdown.
type WidgetKind string

const (
	WidgetCheckbox WidgetKind = "checkbox"
	WidgetInput    WidgetKind = "input"
	WidgetSelect   WidgetKind = "select"
	WidgetRadio    WidgetKind = "radio"
	WidgetField    WidgetKind = "field"
	WidgetAction   WidgetKind = "action"
	WidgetMermaid  WidgetKind = "mermaid"
)

// WidgetSpec is the declarative description of an interactive Markdown control
// together with the exact source span patched on mutation.
type WidgetSpec struct {
	Kind        WidgetKind `json:"kind"`
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Label       string     `json:"label,omitempty"`
	Checked     bool       `json:"checked,omitempty"`
	Value       string     `json:"value,omitempty"`
	InputType   string     `json:"inputType,omitempty"`
	Placeholder string     `json:"placeholder,omitempty"`
	Options     []string   `json:"options,omitempty"`
	Rows        int        `json:"rows,omitempty"`
	Style       string     `json:"style,omitempty"`
	Payload     string     `json:"payload,omitempty"`
	Code        string     `json:"code,omitempty"`
	SourceFile  string     `json:"sourceFile,omitempty"`
	Span        SourceSpan `json:"span"`
	ValueSpan   SourceSpan `json:"valueSpan"`
	Line        int        `json:"line"`
}

// InlineNode is a parsed inline Markdown or widget element.
type InlineNode struct {
	Type     string       `json:"type"` // text, code, strong, em, del, link, image, br, checkbox, input, select, radio, action
	Value    string       `json:"value,omitempty"`
	Href     string       `json:"href,omitempty"`
	Src      string       `json:"src,omitempty"`
	Alt      string       `json:"alt,omitempty"`
	Children []InlineNode `json:"children,omitempty"`
	Widget   *WidgetSpec  `json:"widget,omitempty"`
}

// ListItemNode represents one item in an ordered or unordered list.
type ListItemNode struct {
	Checkbox *WidgetSpec  `json:"checkbox,omitempty"`
	Inlines  []InlineNode `json:"inlines"`
	Children []BlockNode  `json:"children,omitempty"`
	Line     int          `json:"line"`
}

// BlockNode is a parsed block-level Markdown element.
type BlockNode struct {
	Type       string           `json:"type"` // heading, paragraph, hr, blockquote, list, table, code, mermaid, field
	Level      int              `json:"level,omitempty"`
	ID         string           `json:"id,omitempty"`
	Text       string           `json:"text,omitempty"`
	Inlines    []InlineNode     `json:"inlines,omitempty"`
	Alert      string           `json:"alert,omitempty"` // note, tip, important, warning, caution
	Children   []BlockNode      `json:"children,omitempty"`
	Ordered    bool             `json:"ordered,omitempty"`
	Start      int              `json:"start,omitempty"`
	Items      []ListItemNode   `json:"items,omitempty"`
	Headers    [][]InlineNode   `json:"headers,omitempty"`
	Alignments []string         `json:"alignments,omitempty"` // "", "left", "center", "right"
	Rows       [][][]InlineNode `json:"rows,omitempty"`
	Lang       string           `json:"lang,omitempty"`
	Code       string           `json:"code,omitempty"`
	Title      string           `json:"title,omitempty"`
	SourceFile string           `json:"sourceFile,omitempty"`
	Direction  string           `json:"direction,omitempty"`
	Flippable  bool             `json:"flippable,omitempty"`
	Error      string           `json:"error,omitempty"`
	Widget     *WidgetSpec      `json:"widget,omitempty"`
	Span       SourceSpan       `json:"span"`
}

// DocumentStats summarizes interactive and structural counts in a document.
type DocumentStats struct {
	CheckboxTotal   int `json:"checkboxTotal"`
	CheckboxChecked int `json:"checkboxChecked"`
	InputCount      int `json:"inputCount"`
	TableCount      int `json:"tableCount"`
	MermaidCount    int `json:"mermaidCount"`
	LineCount       int `json:"lineCount"`
}

// DocumentIR is the deterministic intermediate representation of a Markdown or Mermaid file.
type DocumentIR struct {
	File     string        `json:"file"`
	Revision string        `json:"revision"`
	Title    string        `json:"title"`
	Headings []HeadingInfo `json:"headings"`
	Blocks   []BlockNode   `json:"blocks"`
	Widgets  []WidgetSpec  `json:"widgets"`
	Stats    DocumentStats `json:"stats"`
}

// ParseOptions configures ParseMarkdown.
type ParseOptions struct {
	File                   string
	ResolveExternalMermaid func(relPath string) (code string, err error)
}

// MutationRequest describes an interactive user action from the browser.
type MutationRequest struct {
	Kind    WidgetKind `json:"kind"`
	ID      string     `json:"id"`
	Checked bool       `json:"checked,omitempty"`
	Value   string     `json:"value,omitempty"`
	Code    string     `json:"code,omitempty"`
}

// PatchResult is returned by ApplyMutation.
type PatchResult struct {
	Changed    bool
	NextSource string
	OldValue   any
	NewValue   any
	Widget     WidgetSpec
}

// ActionEvent is emitted over SSE and stored in memory for every user action.
type ActionEvent struct {
	Seq      int64      `json:"seq"`
	TS       string     `json:"ts"`
	File     string     `json:"file"`
	Kind     WidgetKind `json:"kind"`
	WidgetID string     `json:"widgetId"`
	Name     string     `json:"name"`
	Label    string     `json:"label"`
	Line     int        `json:"line"`
	OldValue any        `json:"oldValue"`
	NewValue any        `json:"newValue"`
	Revision string     `json:"revision"`
}
