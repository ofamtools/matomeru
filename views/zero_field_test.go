package views

import (
	"strings"
	"testing"

	"ofam.dev/ochazuke/ui"
)

func zeroFieldTestSpec(values int) ZeroFieldViewSpec {
	spec := ZeroFieldViewSpec{
		Title:         "Zero-Field Query · Documents",
		Mode:          "filter",
		ModeTone:      "accent",
		SelectedField: "task",
		ActiveSettings: []ZeroFieldSettingPill{
			{ModeLabel: "FILTER", ValueLabel: "task=matomeru", RemoveHint: "xf"},
			{ModeLabel: "GROUP", ValueLabel: "flat list (implicit)", Implicit: true},
		},
		ExprPills:       []ZeroFieldExprPill{{Kind: "clause", Label: "task=matomeru", RemoveHint: "x1"}},
		ModeChips:       []ZeroFieldChip{{Hint: "f", Label: "Filter", Active: true}, {Hint: "g", Label: "Group"}, {Hint: "s", Label: "Sort"}},
		CombinatorChips: []ZeroFieldChip{{Hint: "a", Label: "any"}, {Hint: "&", Label: "AND"}, {Hint: "[", Label: "("}},
		FieldChips:      []ZeroFieldChip{{Hint: "e1", Label: "id"}, {Hint: "e2", Label: "task", Active: true}},
		ActionChips:     []ZeroFieldChip{{Hint: "esc", Label: "apply & close"}},
		MatchedCount:    3,
		TotalCount:      10,
		ScreenWidth:     140,
	}
	for i := range values {
		spec.ValueChips = append(spec.ValueChips, ZeroFieldChip{Hint: string(rune('a' + i)), Label: "◇ project number " + string(rune('A'+i))})
	}
	return spec
}

func zeroFieldBodyText(t *testing.T, node ui.ViewNode) (string, []ui.InlineRun) {
	t.Helper()
	body := node.Find(ZeroFieldID)
	if body == nil {
		t.Fatal("missing stage node")
	}
	runs, _ := body.Props["runs"].([]ui.InlineRun)
	var b strings.Builder
	for _, r := range runs {
		b.WriteString(r.Text + r.Label)
	}
	return b.String(), runs
}

func TestZeroFieldModalViewIsAFixedWidthContentFittedOverlay(t *testing.T) {
	for _, screenW := range []int{80, 140} {
		spec := zeroFieldTestSpec(12)
		spec.ScreenWidth = screenW
		node := ZeroFieldModalView(spec)
		if node.Kind != ui.KindOverlay || node.Scope != ScopeZeroField || node.Facts["zeroField"] != true {
			t.Fatalf("root is not the Zero-Field overlay: %+v", node)
		}
		wantW := min(screenW-4, 112)
		if node.Box.FixedW != wantW || node.Box.Flex != 0 {
			t.Fatalf("overlay box = %+v, want fixed width %d and no flex", node.Box, wantW)
		}
		text, _ := zeroFieldBodyText(t, node)
		lines := strings.Split(text, "\n")
		innerW := wantW - 4
		for _, line := range lines {
			if n := len([]rune(line)); n > innerW {
				t.Fatalf("line wider (%d) than the inner width %d: %q", n, innerW, line)
			}
			if strings.HasPrefix(line, "─") && len([]rune(line)) != innerW {
				t.Fatalf("divider is %d wide, want the inner width %d", len([]rune(line)), innerW)
			}
		}
		valueLines := 0
		for _, line := range lines {
			if strings.Contains(line, "project number") {
				valueLines++
			}
		}
		if valueLines < 2 {
			t.Fatalf("12 values did not wrap onto several rows:\n%s", text)
		}

		root := ui.ViewNode{ID: "root", Kind: ui.KindVStack, Children: []ui.ViewNode{{ID: "page", Kind: ui.KindText, Props: map[string]any{"text": "page"}}, node}}
		solved := ui.Measure(root, ui.DefaultDarkTheme(), ZeroFieldID, screenW, 60)
		rect := solved.Rects[ZeroFieldModalID]
		if rect.W != wantW || rect.H != len(lines)+2 {
			t.Fatalf("overlay rect = %+v, want %d wide and %d tall (content + border)", rect, wantW, len(lines)+2)
		}
	}
}

func TestZeroFieldModalViewDrawsOnlyTheChipsItIsGiven(t *testing.T) {
	spec := zeroFieldTestSpec(0)
	spec.Mode, spec.CombinatorChips, spec.ExprPills = "group", nil, nil
	text, _ := zeroFieldBodyText(t, ZeroFieldModalView(spec))
	for _, absent := range []string{"VALUES", "COMBINE", " AND ", " ) ", "task:"} {
		if strings.Contains(text, absent) {
			t.Fatalf("view draws %q it was not given:\n%s", absent, text)
		}
	}
	for _, want := range []string{"CURRENT", "MODE", "FIELDS", "task=matomeru"} {
		if !strings.Contains(text, want) {
			t.Fatalf("view is missing %q:\n%s", want, text)
		}
	}
}
