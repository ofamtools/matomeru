package views

import (
	"fmt"
	"strings"

	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
)

// RecordRefCardLines is the default height of an auto-hydrating noun/record
// reference card on a ui.page: top border (icon + title + status badge),
// 1 compact interior line (live progress / summary / meta), bottom border.
const RecordRefCardLines = 3

// RecordRefCardSpec is the pure display specification for a 3-line live noun
// reference card. The app layer hydrates it from the store and M5 presentations;
// this presenter performs zero store reads and zero per-noun switches.
type RecordRefCardSpec struct {
	ID          string
	Ref         core.Ref
	Kind        string
	KindLabel   string
	Title       string
	StatusLabel string
	StatusIcon  string
	StatusTone  string
	Summary     string
	ProgressBar []ui.InlineRun
	Progress    string
	Meta        []string
	Lines       int
	Missing     bool
	Unread      bool
	RawFallback bool
}

// RecordRefCardView projects spec into a crisp, 3-line-tall bordered focusable
// ViewNode with top-border title/status badge, a single compact interior line
// of live progress/summary/meta, truthful Facts, and deep-link metadata.
// Explicit [0,1,0,1] padding and FixedH keep the height exact even under
// ui.density="spacious".
func RecordRefCardView(spec RecordRefCardSpec) ui.ViewNode {
	kind := spec.Kind
	if kind == "" {
		kind = spec.Ref.Kind
	}
	ref := spec.Ref
	if ref.Kind == "" {
		ref.Kind = kind
	}
	id := recordRefCardID(spec.ID, ref)
	lines := spec.Lines
	if lines < RecordRefCardLines {
		lines = RecordRefCardLines
	}

	runs, plain := recordRefCardBody(spec)
	bodyProps := map[string]any{"text": plain}
	if len(runs) > 0 {
		bodyProps["runs"] = runs
	}
	bodyNode := ui.ViewNode{
		ID:    id + ".body",
		Kind:  ui.KindText,
		Box:   layout.Constraints{FixedH: 1},
		Props: bodyProps,
	}

	facts := map[string]any{
		"recordCard": true,
		"recordKind": kind,
		"recordId":   ref.ID,
		"status":     spec.StatusLabel,
		"missing":    spec.Missing,
		"deepLink":   recordRefDeepLink(kind, ref.ID),
	}
	if spec.RawFallback {
		facts["hydrated"] = false
	} else {
		facts["hydrated"] = !spec.Missing
	}

	node := ui.ViewNode{
		ID:        id,
		Kind:      ui.KindPanel,
		Scope:     kind,
		Focusable: true,
		Border:    true,
		Unread:    spec.Unread,
		Padding:   [4]int{0, 1, 0, 1},
		Box:       layout.Constraints{FixedH: lines},
		Title:     recordRefCardHeader(spec, kind),
		Tone:      recordRefCardTone(spec),
		Badges:    recordRefCardBadges(spec, kind),
		Facts:     facts,
		Children:  []ui.ViewNode{bodyNode},
	}
	if ref.Kind != "" && ref.ID != "" {
		node.Ref = &ref
	}
	return node
}

func recordRefCardID(id string, ref core.Ref) string {
	if strings.TrimSpace(id) != "" {
		return id
	}
	if ref.Kind != "" && ref.ID != "" {
		return fmt.Sprintf("ref.card.%s.%s", ref.Kind, ref.ID)
	}
	return "ref.card"
}

func recordRefDeepLink(kind, id string) string {
	if kind == "" && id == "" {
		return ""
	}
	return "mtm://" + kind + "/" + id
}

func recordRefCardTone(spec RecordRefCardSpec) string {
	if spec.Missing || spec.RawFallback {
		return "muted"
	}
	return spec.StatusTone
}

func recordRefCardHeader(spec RecordRefCardSpec, kind string) string {
	if spec.Missing {
		label := strings.TrimSpace(spec.Title)
		if label == "" {
			label = fmt.Sprintf("(missing %s)", cmpOrKind(kind))
		}
		return "✕ " + label
	}
	title := strings.TrimSpace(spec.Title)
	if title == "" {
		title = cmpOrKind(spec.KindLabel)
		if title == "" {
			title = cmpOrKind(kind)
		}
	}
	icon := strings.TrimSpace(spec.StatusIcon)
	if icon == "" {
		return title
	}
	if strings.HasPrefix(title, icon+" ") || title == icon {
		return title
	}
	return icon + " " + title
}

func recordRefCardBadges(spec RecordRefCardSpec, kind string) []ui.Badge {
	if spec.Missing {
		return []ui.Badge{{Text: "missing", Tone: "muted"}}
	}
	if spec.RawFallback {
		return []ui.Badge{{Text: cmpOrKind(kind), Tone: "muted"}}
	}
	if strings.TrimSpace(spec.StatusLabel) != "" {
		return []ui.Badge{{Text: spec.StatusLabel, Tone: badgeTone(spec.StatusTone)}}
	}
	if label := strings.TrimSpace(spec.KindLabel); label != "" {
		return []ui.Badge{{Text: strings.ToLower(label), Tone: "info"}}
	}
	return nil
}

func recordRefCardBody(spec RecordRefCardSpec) ([]ui.InlineRun, string) {
	if spec.Missing {
		msg := "record unavailable or deleted"
		return []ui.InlineRun{{Text: msg, Tone: "muted"}}, msg
	}
	if spec.RawFallback {
		msg := strings.TrimSpace(spec.Summary)
		if msg == "" {
			msg = "live hydration off · enter opens"
		}
		return []ui.InlineRun{{Text: msg, Tone: "muted"}}, msg
	}

	var runs []ui.InlineRun
	var plain []string
	appendSegment := func(seg []ui.InlineRun, text string) {
		if len(seg) == 0 {
			return
		}
		if len(runs) > 0 {
			runs = append(runs, ui.InlineRun{Text: " · ", Tone: "muted"})
			plain = append(plain, " · ")
		}
		runs = append(runs, seg...)
		plain = append(plain, text)
	}

	progRuns, progText := recordRefCardProgressRuns(spec)
	appendSegment(progRuns, progText)
	if s := firstLine(spec.Summary, 96); s != "" {
		appendSegment([]ui.InlineRun{{Text: s}}, s)
	}
	for _, m := range spec.Meta {
		if item := firstLine(m, 40); item != "" {
			appendSegment([]ui.InlineRun{{Text: item, Tone: "muted"}}, item)
		}
	}
	if len(runs) == 0 {
		fallback := recordRefEmptyHint(spec)
		return []ui.InlineRun{{Text: fallback, Tone: "muted"}}, fallback
	}
	return runs, strings.Join(plain, "")
}

func recordRefCardProgressRuns(spec RecordRefCardSpec) ([]ui.InlineRun, string) {
	p := strings.TrimSpace(spec.Progress)
	if len(spec.ProgressBar) == 0 && p == "" {
		return nil, ""
	}
	runs := make([]ui.InlineRun, 0, len(spec.ProgressBar)+1)
	var b strings.Builder
	for _, r := range spec.ProgressBar {
		runs = append(runs, r)
		b.WriteString(r.Text)
	}
	if p != "" {
		if len(spec.ProgressBar) > 0 {
			p = " " + p
		}
		runs = append(runs, ui.InlineRun{Text: p, Tone: "accent"})
		b.WriteString(p)
	}
	return runs, b.String()
}

func recordRefEmptyHint(spec RecordRefCardSpec) string {
	label := strings.TrimSpace(spec.KindLabel)
	if label == "" {
		label = cmpOrKind(spec.Kind)
	}
	return strings.ToLower(label) + " · enter opens"
}

func cmpOrKind(kind string) string {
	if strings.TrimSpace(kind) == "" {
		return "record"
	}
	return kind
}
