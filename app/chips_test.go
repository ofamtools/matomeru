package app

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/pathpicker"
)

func TestChipProjectWorktreeFocusAndInheritedPicker(t *testing.T) {
	f := setupProjectWorkspaceFixture(t)
	a, d := f.app, f.driver
	d.Keys("P")
	selectProjectSection(t, f, "worktrees")
	if a.Focus() != projectWorktreesTable {
		t.Fatalf("Worktrees focus = %s", a.Focus())
	}
	d.Keys("j", "g", "X")
	const id = "project.worktrees.chips.target"
	if a.Focus() != id || a.State().Layout.Rects[id].W != 1 {
		t.Fatalf("pointer focus = %s, rect = %+v", a.Focus(), a.State().Layout.Rects[id])
	}
	d.Keys("enter")
	pm := a.activePicker(chipActionsID)
	if pm == nil || !strings.HasPrefix(pm.Model().Title, "Inspect pointer") {
		t.Fatalf("enter opened %q", a.Modal())
	}
	var ids []string
	for _, item := range pm.Model().Items {
		ids = append(ids, item.ID)
		origin, _, _ := strings.Cut(item.Subtitle, " · ")
		want := data.ChipKindWorktree
		if slices.Contains([]string{"cd", "ls", "find", "move", "delete"}, item.ID) {
			want = data.ChipKindDir
		}
		if origin != want {
			t.Errorf("%s origin = %q, want %q", item.ID, origin, want)
		}
	}
	if !slices.Equal(ids, []string{"status", "diff", "remove", "cd", "ls", "find", "move", "delete"}) {
		t.Fatalf("worktree actions = %q", ids)
	}
	preview := pm.Model().PreviewFn(pm.Model().Items[0])
	contents := preview.Find("chip.details.rows")
	var details []string
	for _, row := range contents.Children {
		details = append(details, row.Props["text"].(string))
	}
	if joined := strings.Join(details, "\n"); !strings.Contains(joined, "worktree → dir") || !strings.Contains(joined, "task → Apollo") {
		t.Fatalf("worktree preview = %v", details)
	}
	d.Keys("esc", "esc", "tab", ".")
	pm = a.activePicker(chipActionsID)
	if pm == nil || !strings.Contains(pm.Model().Title, "feat/workspace") {
		t.Fatalf("branch action picker = %q; focus = %q", a.Modal(), a.Focus())
	}
	for _, item := range pm.Model().Items {
		if item.ID == "diff" && strings.HasPrefix(item.Subtitle, "git.ref · ") {
			return
		}
	}
	t.Fatal("branch action picker is missing inherited diff from git.ref")
}

func TestChipFilesPaletteCopyAndOptOut(t *testing.T) {
	a, d := configured(t, t.TempDir())
	path := filepath.Join(t.TempDir(), "readme.md")
	a.recordPathUse(pathpicker.DirEntry{Path: path})
	d.Keys("g", "d")
	if a.Focus() != views.FilesListID {
		t.Fatalf("Files focus = %s", a.Focus())
	}
	d.Keys(":")
	d.Type(CmdChipAct)
	d.Keys("enter")
	pm := a.activePicker(chipActionsID)
	if pm == nil || !strings.Contains(pm.Model().Title, "readme.md") {
		t.Fatalf("palette opened %q; screen:\n%s", a.Modal(), d.Frame())
	}
	d.Keys("j", "j", "enter")
	if a.Modal() != "" || a.Register(core.RegClipboard) != path {
		t.Fatalf("copy path: modal=%q clipboard=%q", a.Modal(), a.Register(core.RegClipboard))
	}
	a.cfg.userSettings[SetLivePointerChips] = false
	a.reloadSettings()
	d.Keys("g", "X")
	root := a.View(a.State())
	if a.Focus() != views.FilesListID || root.Find("files.chips") != nil {
		t.Fatal("opt-out still exposes Files chips or their focus binding")
	}
}

func TestChipPRPreservesURLAndSourceBinding(t *testing.T) {
	f := setupProjectWorkspaceFixture(t)
	a, d := f.app, f.driver
	value := "https://github.com/ofam/apollo/pull/81#discussion_r2"
	rec, _, err := a.Store.AddOfficialProjectContext(data.OfficialContextInput{
		ProjectID: f.proj.ID, Title: "PR with discussion", Value: value, ContextKind: data.ChipKindPR,
	}, User)
	if err != nil {
		t.Fatal(err)
	}
	d.Keys("P")
	selectProjectSection(t, f, "prs")
	d.Keys("/")
	d.Type("PR with discussion")
	d.Keys("enter")
	root := a.View(a.State())
	rowChain := ui.ContextChain(root, a.Focus(), nil)
	resolved := a.Engine.Keymap.Resolve(core.KeyQuery{Mode: ModeNormal, Chain: rowChain, Key: "enter"})
	if resolved.Command == nil || resolved.Command.ID != CmdProjectContextOpen {
		t.Fatalf("PR table Enter = %+v", resolved.Command)
	}
	d.Keys("g", "X")
	chip, got := pointerChipAt(a.View(a.State()), a.Focus())
	if !got || !chip.Compact || chip.Ref != rec.Ref() || chip.TargetID != value || a.Focus() != "project.prs.chips.target" {
		t.Fatalf("focused PR chip = %+v, focus = %q", chip, a.Focus())
	}
	dot := a.Engine.Keymap.Resolve(core.KeyQuery{Mode: ModePicker, Chain: []core.ContextFrame{{Scope: "path"}}, Key: "."})
	if dot.Command == nil || dot.Command.ID != pathpicker.CmdHiddenToggle {
		t.Fatalf("path picker dot = %+v", dot.Command)
	}
}
