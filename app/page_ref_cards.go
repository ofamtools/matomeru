package app

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

func (a *App) pageHydrateRefCardsEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetPageHydrateRefCards, true)
}

// hydratePageRefCards walks root and expands every declarative noun/record ref
// card node (kind "record.card"/"ref.card", or kind "card" with ref +
// props.recordCard=true / props.hydrateRef=true) into a live 3-line bordered
// card backed by current store state and M5 noun presentations.
func (a *App) hydratePageRefCards(root *ui.ViewNode) {
	if root == nil {
		return
	}
	hydrate := a.pageHydrateRefCardsEnabled()
	var walk func(n *ui.ViewNode, path string)
	walk = func(n *ui.ViewNode, path string) {
		if mcpui.IsRecordCardNode(n) {
			*n = a.hydrateOneRecordRefCard(n, path, hydrate)
			return
		}
		for i := range n.Children {
			childPath := fmt.Sprintf("%d", i)
			if path != "" {
				childPath = path + "." + childPath
			}
			walk(&n.Children[i], childPath)
		}
	}
	walk(root, "")
}

func (a *App) hydrateOneRecordRefCard(n *ui.ViewNode, path string, hydrate bool) ui.ViewNode {
	id := strings.TrimSpace(n.ID)
	if id == "" {
		id = "ref.card." + path
	}
	lines := recordCardPropLines(n.Props)
	if n.Ref == nil || n.Ref.Kind == "" || n.Ref.ID == "" {
		return views.RecordRefCardView(views.RecordRefCardSpec{
			ID:      id,
			Title:   strings.TrimSpace(n.Title),
			Lines:   lines,
			Missing: true,
		})
	}
	ref := *n.Ref
	if !hydrate || a.Store == nil {
		return views.RecordRefCardView(a.rawRecordRefCardSpec(id, n, ref, lines))
	}
	return views.RecordRefCardView(a.liveRecordRefCardSpec(id, ref, lines))
}

func (a *App) rawRecordRefCardSpec(id string, n *ui.ViewNode, ref core.Ref, lines int) views.RecordRefCardSpec {
	schema := a.schema(ref.Kind)
	title := strings.TrimSpace(n.Title)
	if title == "" {
		title = views.KindLabel(schema, data.Record{Kind: ref.Kind})
	}
	return views.RecordRefCardSpec{
		ID:          id,
		Ref:         ref,
		Kind:        ref.Kind,
		KindLabel:   views.KindLabel(schema, data.Record{Kind: ref.Kind}),
		Title:       title,
		Summary:     recordCardPropSummary(n.Props),
		Lines:       lines,
		RawFallback: true,
	}
}

func (a *App) liveRecordRefCardSpec(id string, ref core.Ref, lines int) views.RecordRefCardSpec {
	schema := a.schema(ref.Kind)
	kindLabel := views.KindLabel(schema, data.Record{Kind: ref.Kind})
	rec, err := a.Store.Get(ref)
	pres := a.presentations().ResolveRef(ref, rec, schema, data.FormCard)
	if err != nil || rec == nil {
		return views.RecordRefCardSpec{
			ID:        id,
			Ref:       ref,
			Kind:      ref.Kind,
			KindLabel: kindLabel,
			Title:     pres.Label,
			Lines:     lines,
			Missing:   true,
		}
	}

	statusLabel, statusIcon, statusTone := resolveRecordCardStatus(schema, *rec, pres)
	spec := views.RecordRefCardSpec{
		ID:          id,
		Ref:         ref,
		Kind:        ref.Kind,
		KindLabel:   kindLabel,
		Title:       recordCardTitle(schema, *rec, pres),
		StatusLabel: statusLabel,
		StatusIcon:  statusIcon,
		StatusTone:  statusTone,
		Summary:     recordCardSummary(*rec, pres),
		Meta:        a.recordCardMeta(*rec, pres),
		Lines:       lines,
		Unread:      rec.IsUnreadBy(schema.UnreadField),
	}
	if prog, ok := a.Store.RecordProgress(*rec); ok {
		spec.ProgressBar = views.CompactLaneBarRuns(prog.Done, prog.Active, prog.Planned, 4)
		spec.Progress = formatRecordCardProgress(prog)
	}
	return spec
}

func recordCardTitle(schema data.Schema, rec data.Record, pres data.ResolvedPresentation) string {
	if s := strings.TrimSpace(pres.Label); s != "" {
		return s
	}
	return views.Title(schema, rec)
}

func resolveRecordCardStatus(schema data.Schema, rec data.Record, pres data.ResolvedPresentation) (label, icon, tone string) {
	label, icon, tone, _ = data.RecordPrimaryStatus(schema, rec)
	if icon == "" {
		icon = pres.Icon
	}
	if tone == "" {
		tone = pres.Tone
	}
	if label == "" && len(pres.Badges) > 0 {
		label = pres.Badges[0].Text
		if tone == "" {
			tone = pres.Badges[0].Tone
		}
	}
	return label, icon, tone
}

func recordCardSummary(rec data.Record, pres data.ResolvedPresentation) string {
	if s := strings.TrimSpace(pres.Subtitle); s != "" {
		return s
	}
	for _, field := range []string{"summary", "description", "body", "command", "cwd"} {
		if s := strings.TrimSpace(rec.Text(field)); s != "" {
			return s
		}
	}
	return ""
}

func (a *App) recordCardMeta(rec data.Record, pres data.ResolvedPresentation) []string {
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || slices.Contains(out, s) {
			return
		}
		out = append(out, s)
	}
	if branch := strings.TrimSpace(rec.Text("branch")); branch != "" {
		add(branch)
	}
	if parentRef, ok := data.RecordParentRef(rec); ok {
		if label := a.recordRefChipLabel(parentRef); label != "" {
			add(label)
		}
	}
	for _, m := range pres.Meta {
		if len(out) >= 2 {
			break
		}
		if strings.Contains(m, "/") && filepath.IsAbs(m) {
			m = filepath.Base(m)
		}
		add(m)
	}
	if age := formatCompactAge(rec.UpdatedAt); age != "" && len(out) < 3 {
		add(age)
	}
	return out
}

func (a *App) recordRefChipLabel(ref core.Ref) string {
	if a.Store == nil {
		return ""
	}
	rec, err := a.Store.Get(ref)
	if err != nil || rec == nil {
		return ""
	}
	pres := a.presentations().ResolveRef(ref, rec, a.schema(ref.Kind), data.FormChip)
	if pres.Missing {
		return ""
	}
	return pres.Label
}

func formatRecordCardProgress(p data.RecordProgressSummary) string {
	text := fmt.Sprintf("%d/%d", p.Done, p.Total())
	if p.Active > 0 {
		text += fmt.Sprintf(" ▶%d", p.Active)
	}
	return text
}

func formatCompactAge(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func recordCardPropLines(props map[string]any) int {
	if props == nil {
		return views.RecordRefCardLines
	}
	switch v := props["lines"].(type) {
	case int:
		return max(v, views.RecordRefCardLines)
	case int64:
		return max(int(v), views.RecordRefCardLines)
	case float64:
		return max(int(v), views.RecordRefCardLines)
	default:
		return views.RecordRefCardLines
	}
}

func recordCardPropSummary(props map[string]any) string {
	if props == nil {
		return ""
	}
	s, _ := props["summary"].(string)
	return strings.TrimSpace(s)
}

// openRecordTarget opens ref from views.CmdOpen. When focused on a hydrated
// record ref card inside a ui.page, pane and ui.page refs deep-link straight
// to their interactive surface while every other noun opens its Show page.
func (a *App) openRecordTarget(s core.State, ref core.Ref) core.State {
	if a.focusedIsRecordCard(s) {
		openers := map[string]func(core.State, string) core.State{
			"pane":    func(st core.State, id string) core.State { return a.jumpToPane(withMode(st, ModeNormal), id, false) },
			"ui.page": func(st core.State, id string) core.State { return a.openCanvasRefPage(st, id) },
		}
		if open := openers[ref.Kind]; open != nil {
			return open(s, ref.ID)
		}
	}
	return a.show(s, ref)
}

func (a *App) focusedIsRecordCard(s core.State) bool {
	focusID := s.ActiveFrame().FocusID
	if focusID == "" {
		return false
	}
	root := a.View(s)
	n := root.Find(focusID)
	return n != nil && n.Facts["recordCard"] == true
}
