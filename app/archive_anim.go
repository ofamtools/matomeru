package app

import (
	"fmt"
	"slices"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/table"
	"ofam.dev/ochazuke/widgets/tree"
)

const effectArchiveAnimTick = "archive_anim_tick"

type archiveAnimController struct {
	set  *traits.ExitAnimSet
	refs map[string]core.Ref
	gen  uint64
}

func newArchiveAnimController() *archiveAnimController {
	return &archiveAnimController{
		set:  traits.NewExitAnimSet(traits.DefaultExitAnimConfig()),
		refs: map[string]core.Ref{},
	}
}

func (a *App) archiveAnimEnabled() bool {
	if a == nil || a.cfg == nil || a.cfg.Settings == nil {
		return true
	}
	return a.cfg.Settings.Bool(SetArchiveAnimation, true)
}

func (a *App) archiveAnimConfig() traits.ExitAnimConfig {
	cfg := traits.DefaultExitAnimConfig()
	if a != nil && a.cfg != nil && a.cfg.Settings != nil {
		if ms := a.cfg.Settings.Int(SetArchiveAnimationMs, int(traits.DefaultExitAnimDuration/time.Millisecond)); ms > 0 {
			cfg.Duration = time.Duration(ms) * time.Millisecond
		}
	}
	return traits.NormalizeExitAnimConfig(cfg)
}

func (a *App) ensureArchiveAnim() *archiveAnimController {
	if a.archiveAnim == nil {
		a.archiveAnim = newArchiveAnimController()
	}
	return a.archiveAnim
}

func (a *App) invalidateArchiveAnimCaches() {
	if a.panes != nil {
		a.panes.railCacheKey = ""
	}
	if a.nav != nil && a.nav.recCacheKeys != nil {
		clear(a.nav.recCacheKeys)
	}
}

func (a *App) startArchiveAnim(ref core.Ref) core.Effect {
	if ref.Kind == "" || ref.ID == "" {
		return nil
	}
	ac := a.ensureArchiveAnim()
	ac.set.Config = a.archiveAnimConfig()
	key := data.RefText(ref)
	ac.set.Start(key, a.now())
	ac.refs[key] = ref
	ac.gen++
	a.invalidateArchiveAnimCaches()
	return a.archiveAnimTickEffect()
}

func (a *App) cancelArchiveAnim(ref core.Ref) bool {
	if a.archiveAnim == nil || a.archiveAnim.set.Len() == 0 {
		return false
	}
	key := data.RefText(ref)
	removed := a.archiveAnim.set.Cancel(key)
	delete(a.archiveAnim.refs, key)
	if removed {
		a.archiveAnim.gen++
		a.invalidateArchiveAnimCaches()
	}
	return removed
}

func (a *App) reconcileArchiveAnims() {
	if a.archiveAnim == nil || a.archiveAnim.set.Len() == 0 || a.Store == nil {
		return
	}
	changed := false
	for key, ref := range a.archiveAnim.refs {
		rec, err := a.Store.Get(ref)
		if err != nil || rec == nil || !rec.IsArchived() {
			a.archiveAnim.set.Cancel(key)
			delete(a.archiveAnim.refs, key)
			changed = true
		}
	}
	if changed {
		a.archiveAnim.gen++
		a.invalidateArchiveAnimCaches()
	}
}

func (a *App) archiveAnimTickEffect() core.Effect {
	if a.archiveAnim == nil {
		return nil
	}
	delay, ok := a.archiveAnim.set.NextTick(a.now())
	if !ok {
		return nil
	}
	if delay <= 0 {
		delay = time.Millisecond
	}
	return core.CustomEffect{Kind: effectArchiveAnimTick, Data: delay}
}

func (a *App) tickArchiveAnim() (bool, core.Effect) {
	if a.archiveAnim == nil || a.archiveAnim.set.Len() == 0 {
		return false, nil
	}
	expired, stillActive := a.archiveAnim.set.Prune(a.now())
	for _, key := range expired {
		delete(a.archiveAnim.refs, key)
	}
	a.archiveAnim.gen++
	a.invalidateArchiveAnimCaches()
	if !stillActive {
		return true, nil
	}
	return true, a.archiveAnimTickEffect()
}

func (a *App) archiveAnimCacheKey() string {
	if a.archiveAnim == nil || a.archiveAnim.set.Len() == 0 {
		return ""
	}
	if a.opts.Now != nil {
		return fmt.Sprintf("%d:%d", a.archiveAnim.gen, a.now().UnixNano())
	}
	return fmt.Sprintf("%d", a.archiveAnim.gen)
}

func (a *App) archiveAnimActive(ref core.Ref) bool {
	if a.archiveAnim == nil || a.archiveAnim.set.Len() == 0 || a.nav.showArchived {
		return false
	}
	return a.archiveAnim.set.Active(data.RefText(ref), a.now())
}

func (a *App) archiveAnimProgress(ref core.Ref) (float64, bool) {
	if a.archiveAnim == nil || a.archiveAnim.set.Len() == 0 || a.nav.showArchived {
		return 0, false
	}
	key := data.RefText(ref)
	now := a.now()
	if !a.archiveAnim.set.Active(key, now) {
		return 0, false
	}
	return a.archiveAnim.set.Progress(key, now)
}

func (a *App) hasAnimatingKind(kind string) bool {
	if a.archiveAnim == nil || a.archiveAnim.set.Len() == 0 || a.nav.showArchived {
		return false
	}
	now := a.now()
	for key, ref := range a.archiveAnim.refs {
		if ref.Kind == kind && a.archiveAnim.set.Active(key, now) {
			return true
		}
	}
	return false
}

func (a *App) appendAnimatingRecords(kind string, items []data.Record) []data.Record {
	if !a.hasAnimatingKind(kind) || a.Store == nil {
		return items
	}
	seen := make(map[string]bool, len(items))
	for _, rec := range items {
		seen[data.RefText(rec.Ref())] = true
	}
	keys := make([]string, 0, len(a.archiveAnim.refs))
	now := a.now()
	for key, ref := range a.archiveAnim.refs {
		if ref.Kind == kind && !seen[key] && a.archiveAnim.set.Active(key, now) {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return items
	}
	slices.Sort(keys)
	out := slices.Clone(items)
	for _, key := range keys {
		ref := a.archiveAnim.refs[key]
		rec, err := a.Store.Get(ref)
		if err == nil && rec != nil {
			out = append(out, *rec)
		}
	}
	return out
}

func exitSpansToToneSpans(spans []traits.ExitSpan) []ui.ToneSpan {
	if len(spans) == 0 {
		return nil
	}
	out := make([]ui.ToneSpan, len(spans))
	for i, sp := range spans {
		out[i] = ui.ToneSpan{From: sp.From, To: sp.To, Tone: sp.Tone}
	}
	return out
}

func (a *App) applyArchiveAnimToRow(row *table.Row, titleCol, title string, ref core.Ref) {
	if row == nil || titleCol == "" {
		return
	}
	prog, ok := a.archiveAnimProgress(ref)
	if !ok {
		return
	}
	vis, spans := traits.DissolveText(title, prog, a.archiveAnimConfig())
	row.Tone = "row.dim"
	if row.Cells == nil {
		row.Cells = map[string]any{}
	}
	row.Cells[titleCol] = vis
	if len(spans) > 0 {
		if row.CellSpans == nil {
			row.CellSpans = map[string][]ui.ToneSpan{}
		}
		row.CellSpans[titleCol] = exitSpansToToneSpans(spans)
	} else if row.CellSpans != nil {
		delete(row.CellSpans, titleCol)
	}
	if row.Facts == nil {
		row.Facts = map[string]any{}
	}
	row.Facts[data.ColArchiveStatus] = data.ArchiveArchived
}

func (a *App) applyArchiveAnimToTreeNode(rec data.Record, nd *tree.Node) {
	if nd == nil {
		return
	}
	prog, ok := a.archiveAnimProgress(rec.Ref())
	if !ok {
		return
	}
	vis, spans := traits.DissolveText(nd.Label, prog, a.archiveAnimConfig())
	nd.Tone = "row.dim"
	nd.Label = vis
	nd.LabelSpans = exitSpansToToneSpans(spans)
	if nd.Facts == nil {
		nd.Facts = map[string]any{}
	}
	nd.Facts[data.ColArchiveStatus] = data.ArchiveArchived
}

func (a *App) recordTreeNodesWithAnimating(kind string) ([]data.TreeNode, error) {
	if !a.hasAnimatingKind(kind) {
		return a.Store.RecordTree(kind, a.nav.showArchived)
	}
	full, err := a.Store.RecordTree(kind, true)
	if err != nil {
		return nil, err
	}
	var filter func(ns []data.TreeNode) []data.TreeNode
	filter = func(ns []data.TreeNode) []data.TreeNode {
		var out []data.TreeNode
		for _, nd := range ns {
			animating := a.archiveAnimActive(nd.Record.Ref())
			if nd.Record.IsArchived() && !animating {
				continue
			}
			cp := nd
			if animating {
				cp.Children = nil
			} else {
				cp.Children = filter(nd.Children)
			}
			out = append(out, cp)
		}
		return out
	}
	return filter(full), nil
}
