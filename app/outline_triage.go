package app

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/picker"
	"ofam.dev/ochazuke/widgets/toast"
)

var knownShellBinaries = map[string]bool{
	"sh":      true,
	"bash":    true,
	"zsh":     true,
	"fish":    true,
	"dash":    true,
	"csh":     true,
	"tcsh":    true,
	"ksh":     true,
	"nu":      true,
	"nushell": true,
	"shell":   true,
	"vim":     true,
	"nvim":    true,
}

// formatOutlineSubtree serializes a TreeNode subtree into clean indented
// Markdown (titles, IDs, primary status / workStatus, priority, and
// description/notes if present). Pure function with zero store side effects.
func formatOutlineSubtree(schema data.Schema, root data.TreeNode) string {
	var b strings.Builder
	statusCol, hasStatus := primaryStatusColumn(schema)

	var walk func(n data.TreeNode, depth int)
	walk = func(n data.TreeNode, depth int) {
		rec := n.Record
		kind := rec.Kind
		if kind == "" {
			kind = schema.Kind
		}
		title := strings.TrimSpace(views.Title(schema, rec))
		if title == "" {
			title = rec.ID
		}
		indent := strings.Repeat("  ", depth)
		var meta []string
		meta = append(meta, fmt.Sprintf("`%s:%s`", kind, rec.ID))
		if hasStatus {
			st := strings.TrimSpace(rec.Text(statusCol.Name))
			if st == "" {
				st = statusCol.Default
			}
			if st != "" {
				meta = append(meta, "status: `"+st+"`")
			}
		} else if ws := strings.TrimSpace(rec.Text("workStatus")); ws != "" {
			meta = append(meta, "status: `"+ws+"`")
		}
		if prio := strings.TrimSpace(rec.Text("priority")); prio != "" {
			meta = append(meta, "priority: `"+prio+"`")
		}

		if depth == 0 {
			fmt.Fprintf(&b, "%s- **%s** (%s)\n", indent, title, strings.Join(meta, ", "))
		} else {
			fmt.Fprintf(&b, "%s- %s (%s)\n", indent, title, strings.Join(meta, ", "))
		}

		detailIndent := indent + "  "
		for _, spec := range []struct {
			field string
			label string
		}{
			{"description", "Description"},
			{"notes", "Notes"},
			{"summary", "Summary"},
		} {
			val := strings.TrimSpace(rec.Text(spec.field))
			if val == "" {
				continue
			}
			lines := strings.Split(val, "\n")
			fmt.Fprintf(&b, "%s%s: %s\n", detailIndent, spec.label, strings.TrimSpace(lines[0]))
			for _, extra := range lines[1:] {
				fmt.Fprintf(&b, "%s%s\n", detailIndent, strings.TrimRight(extra, " \t"))
			}
		}

		for _, child := range n.Children {
			walk(child, depth+1)
		}
	}

	walk(root, 0)
	return strings.TrimRight(b.String(), "\n")
}

// buildOutlineTriagePrompt constructs the read-only brain-dump triage prompt
// sent to a target agent pane.
func buildOutlineTriagePrompt(schema data.Schema, root data.TreeNode, pane data.Record) string {
	rec := root.Record
	kind := rec.Kind
	if kind == "" {
		kind = schema.Kind
	}
	refStr := data.RefText(core.Ref{Kind: kind, ID: rec.ID})
	title := strings.TrimSpace(views.Title(schema, rec))
	if title == "" {
		title = rec.ID
	}
	outlineMD := formatOutlineSubtree(schema, root)

	var b strings.Builder
	fmt.Fprintf(&b, "Please triage and organize this brain-dump outline (%q, %s).\n\n", title, refStr)
	b.WriteString("## Raw Outline\n\n")
	b.WriteString(outlineMD)
	b.WriteString("\n\n## Instructions (STRICT READ-ONLY PROPOSAL)\n")
	b.WriteString("1. Inspect the outline above: group related items, clarify ambiguous bullets, suggest priorities/ordering, and flag open questions or dependencies.\n")
	b.WriteString("2. **DO NOT modify, create, edit, move, or archive any owner tasks** (or other store records). Keep this strictly read-only.\n")
	fmt.Fprintf(&b, "3. Publish your structured proposal as a dedicated **`ui.page`** via `ui.create_page` (using `kind: \"markdown\"`, linked to task `%s` and pane `%s`).\n", refStr, pane.ID)
	b.WriteString("4. Optionally ask for owner approval or clarifying choices via `ui.ask` before any task mutations are applied.\n")
	return b.String()
}

func findTreeSubtreeWithAncestors(nodes []data.TreeNode, targetID string) (node data.TreeNode, ancestorIDs []string, ok bool) {
	var walk func(ns []data.TreeNode, chain []string) (data.TreeNode, []string, bool)
	walk = func(ns []data.TreeNode, chain []string) (data.TreeNode, []string, bool) {
		for _, n := range ns {
			nextChain := append([]string{n.Record.ID}, chain...)
			if n.Record.ID == targetID {
				return n, nextChain, true
			}
			if found, anc, hit := walk(n.Children, nextChain); hit {
				return found, anc, true
			}
		}
		return data.TreeNode{}, nil, false
	}
	return walk(nodes, nil)
}

func (a *App) isLiveAgentPane(rec data.Record) bool {
	if rec.Kind != data.KindPane || rec.ID == "" || rec.IsArchived() {
		return false
	}
	if role := strings.TrimSpace(rec.Text("role")); role != "" && role != data.RoleNormal {
		return false
	}
	status := strings.ToLower(strings.TrimSpace(rec.Text("runStatus")))
	if status == "dead" || status == "exited" {
		return false
	}
	if pt := a.paneTelemetry(rec.ID); pt != nil {
		st := strings.ToLower(strings.TrimSpace(pt.State))
		if st == "dead" || st == "exited" {
			return false
		}
	}
	hName := strings.ToLower(strings.TrimSpace(rec.Text("harness")))
	if hName != "" {
		if entry, ok := a.lookupHarness(hName); ok {
			return !entry.Shell
		}
		if knownShellBinaries[hName] {
			return false
		}
		return true
	}
	cmd := strings.TrimSpace(rec.Text("command"))
	if cmd == "" {
		return false
	}
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return false
	}
	base := strings.ToLower(filepath.Base(fields[0]))
	return !knownShellBinaries[base]
}

func (a *App) liveAgentPanes() []data.Record {
	if a.Store == nil {
		return nil
	}
	res, err := a.Store.Query(data.Query{Kind: data.KindPane, IncludeArchived: false})
	if err != nil {
		return nil
	}
	var out []data.Record
	for _, p := range res.Items {
		if a.isLiveAgentPane(p) {
			out = append(out, p)
		}
	}
	slices.SortStableFunc(out, func(x, y data.Record) int {
		return y.Time("promptedAt").Compare(x.Time("promptedAt"))
	})
	return out
}

func (a *App) resolveOutlineTriagePane(kind string, ancestorIDs []string, explicitArg string) (target *data.Record, candidates []data.Record, errMsg string) {
	live := a.liveAgentPanes()

	// 1. Explicit pane argument (ID, pane:<id>, or title).
	if want := strings.TrimSpace(explicitArg); want != "" {
		wantID := strings.TrimPrefix(want, data.KindPane+":")
		for i := range live {
			p := live[i]
			if p.ID == wantID || strings.EqualFold(views.Title(a.schema(data.KindPane), p), want) {
				cp := p
				return &cp, nil, ""
			}
		}
		return nil, nil, fmt.Sprintf("live agent pane %q not found", want)
	}

	if len(live) == 0 {
		return nil, nil, "no live agent pane found — launch an agent pane first"
	}

	selPane, hasSel := a.SelectedPane()
	if hasSel && selPane != nil && !a.isLiveAgentPane(*selPane) {
		selPane, hasSel = nil, false
	}

	// 2. Live non-shell agent pane associated with the focused task or any ancestor task.
	if kind == data.KindTask && len(ancestorIDs) > 0 {
		for _, ancID := range ancestorIDs {
			var matched []data.Record
			for _, p := range live {
				if taskRef, ok := p.RefField("task"); ok && taskRef.Kind == data.KindTask && taskRef.ID == ancID {
					matched = append(matched, p)
				}
			}
			if len(matched) == 1 {
				cp := matched[0]
				return &cp, nil, ""
			}
			if len(matched) > 1 {
				if hasSel {
					for i := range matched {
						if matched[i].ID == selPane.ID {
							cp := matched[i]
							return &cp, nil, ""
						}
					}
				}
				return nil, matched, ""
			}
		}
	}

	// 3. Currently selected live non-shell agent pane.
	if hasSel && selPane != nil {
		cp := *selPane
		return &cp, nil, ""
	}

	// 4. Sole live non-shell agent pane.
	if len(live) == 1 {
		cp := live[0]
		return &cp, nil, ""
	}

	// 5. Multiple ambiguous live agent panes -> caller opens picker.
	return nil, live, ""
}

func (a *App) sendOutlineTriage(schema data.Schema, root data.TreeNode, pane data.Record) {
	prompt := buildOutlineTriagePrompt(schema, root, pane)
	a.deliverDispatchedPrompt(pane, prompt)
	paneTitle := views.Title(a.schema(data.KindPane), pane)
	outlineTitle := views.Title(schema, root.Record)
	msg := fmt.Sprintf("Sent %q to %s for read-only triage", outlineTitle, paneTitle)
	a.say("%s", msg)
	a.pushToast(msg, toast.Ok)
}

func (a *App) outlineTriage(s core.State, c core.Command) core.StepResult {
	var (
		kind     string
		targetID string
	)
	if tm, k, treeID, _, ok := a.activeTree(s); ok {
		a.syncTreeModel(tm, k, treeID)
		row, has := tm.Focused()
		if !has || row.Draft || row.ID == "" {
			a.say("no outline item selected")
			return core.Next(s)
		}
		kind = k
		targetID = row.ID
	} else if ref, ok := a.subject(s, c); ok && ref.ID != "" {
		kind = ref.Kind
		targetID = ref.ID
	} else {
		a.say("no outline item selected")
		return core.Next(s)
	}

	schema := a.schema(kind)
	nodes, err := a.recordTreeNodes(kind)
	if err != nil {
		a.fail("outline triage", err)
		return core.Next(s)
	}
	subtree, ancestors, ok := findTreeSubtreeWithAncestors(nodes, targetID)
	if !ok {
		a.say("outline item not found")
		return core.Next(s)
	}

	target, candidates, errMsg := a.resolveOutlineTriagePane(kind, ancestors, c.Arg("pane"))
	if errMsg != "" {
		a.say("%s", errMsg)
		a.pushToast(errMsg, toast.Warn)
		return core.Next(s)
	}
	if target != nil {
		a.sendOutlineTriage(schema, subtree, *target)
		return core.Next(s)
	}

	// Multiple ambiguous live agent panes: open a quick picker.
	paneSchema := a.schema(data.KindPane)
	items := make([]picker.Item, 0, len(candidates))
	for _, p := range candidates {
		r := p.Ref()
		title := views.Title(paneSchema, p)
		var sub []string
		if h := strings.TrimSpace(p.Text("harness")); h != "" {
			sub = append(sub, h)
		}
		if tRef, hasTask := p.RefField("task"); hasTask {
			sub = append(sub, a.title(tRef))
		}
		items = append(items, picker.Item{
			ID:       p.ID,
			Title:    title,
			Subtitle: strings.Join(sub, " · "),
			Ref:      &r,
			Scope:    data.KindPane,
			Badges:   views.StatusBadges(paneSchema, p),
			Keywords: []string{title, p.ID, p.Text("harness")},
			Data:     p,
		})
	}
	pm := picker.New("Send outline to agent pane", items)
	pm.Placeholder = "pick an agent pane for read-only triage"
	m := a.newPickerModal(pm, modal.PickerOptions{
		ID:       "outline.triage.pane",
		PageSize: 8,
		OnConfirm: func(picked picker.Item) modal.Result {
			paneRec, ok := picked.Data.(data.Record)
			if !ok && picked.Ref != nil {
				if rec, err := a.Store.Get(*picked.Ref); err == nil && rec != nil {
					paneRec = *rec
					ok = true
				}
			}
			if ok {
				a.sendOutlineTriage(schema, subtree, paneRec)
			}
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	})
	a.say("pick an agent pane for read-only triage")
	return core.Next(a.pushModal(s, m))
}
