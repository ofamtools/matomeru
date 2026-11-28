package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/pathpicker"
)

func matomeruCheckoutWithCodeFS(home, repoDir string) (featuresTestFS, func(string) ([]byte, error)) {
	fs := matomeruCheckoutFS(home, repoDir)
	stylePath := filepath.Join(repoDir, "views", "style.go")
	lines := make([]string, 230)
	lines[0] = "package views"
	for i := 1; i < len(lines); i++ {
		lines[i] = "// line"
	}
	lines[192] = "func StatusIndicator(schema data.Schema, colName, value string) (icon, label, tone, rowTone string) {"
	lines[193] = "\treturn \"●\", value, \"accent\", \"\""
	lines[194] = "}"
	fs.content[stylePath] = strings.Join(lines, "\n") + "\n"
	fs.dirs[filepath.Join(repoDir, "views")] = []pathpicker.DirEntry{
		{Name: "style.go", Path: stylePath, Size: int64(len(fs.content[stylePath]))},
	}
	readFile := func(p string) ([]byte, error) {
		if txt, ok := fs.content[filepath.Clean(p)]; ok {
			return []byte(txt), nil
		}
		return nil, os.ErrNotExist
	}
	return fs, readFile
}

func TestFeaturesDomainNavEverythingIndicatorsAndOptOut(t *testing.T) {
	fs, readFile := matomeruCheckoutWithCodeFS("/home/dev", "/home/dev/proj/matomeru")
	a, d := newFeaturesTestApp(t, Options{FS: fs, ReadFile: readFile})

	d.Keys("4")
	if a.Space() != SpaceFeatures {
		t.Fatalf("slot 4 did not open SpaceFeatures: %q", a.Space())
	}

	snap := a.collectFeaturesSnapshot()
	navGroups, valid := a.buildFeaturesNav(snap, a.featuresCtrl().activeItemKey())
	domIdx := slices.IndexFunc(navGroups, func(g views.FeaturesNavGroup) bool { return g.Key == featuresDomainNavGroup })
	if domIdx < 0 || navGroups[domIdx].Title != "Domain" {
		t.Fatalf("missing Domain nav group: %+v", navGroups)
	}

	for _, wantKey := range []string{
		"domain:everything",
		"domain:indicators",
		"domain:components",
		"domain:badges",
		"domain:signals",
		"domain:verbs",
		"domain:modes",
		"domain:views",
	} {
		if !valid[wantKey] {
			t.Fatalf("Domain nav missing %q", wantKey)
		}
	}

	// Everything page contains every classification group with non-empty occurrences and real file:start-end locations.
	everything := a.buildFeaturesDetail(snap, "domain:everything")
	if everything.Title != "Everything" || len(everything.DomainGroups) < 7 {
		t.Fatalf("Everything detail unexpected: title=%q groups=%d", everything.Title, len(everything.DomainGroups))
	}
	for _, grp := range everything.DomainGroups {
		if len(grp.Items) == 0 {
			t.Errorf("Everything group %q has 0 occurrences", grp.Key)
		}
		for _, item := range grp.Items {
			if !item.Location.Valid() || item.Location.StartLine <= 0 {
				t.Errorf("occurrence %q missing verified CodeLocation: %+v", item.ID, item.Location)
			}
		}
	}

	// Indicators page is a complete, non-segmented first-class view.
	indDetail := a.buildFeaturesDetail(snap, "domain:indicators")
	if indDetail.Title != "Indicators" || len(indDetail.DomainGroups) != 1 || len(indDetail.DomainGroups[0].Items) < 8 {
		t.Fatalf("Indicators detail unexpected: %+v", indDetail)
	}

	// Per-page & per-noun cross-referencing attaches Related Indicators / Related Components automatically.
	panesDetail := a.buildFeaturesDetail(snap, "page:"+SpacePanes)
	if !slices.ContainsFunc(panesDetail.DomainGroups, func(g views.DomainGroup) bool { return g.Title == "Related Indicators" && len(g.Items) > 0 }) {
		t.Fatalf("page:panes missing Related Indicators section: %+v", panesDetail.DomainGroups)
	}
	if !slices.ContainsFunc(panesDetail.DomainGroups, func(g views.DomainGroup) bool { return g.Title == "Related Components" && len(g.Items) > 0 }) {
		t.Fatalf("page:panes missing Related Components section: %+v", panesDetail.DomainGroups)
	}

	taskDetail := a.buildFeaturesDetail(snap, "noun:"+data.KindTask)
	if !slices.ContainsFunc(taskDetail.DomainGroups, func(g views.DomainGroup) bool { return g.Title == "Related Indicators" && len(g.Items) > 0 }) {
		t.Fatalf("noun:task missing Related Indicators section: %+v", taskDetail.DomainGroups)
	}

	// Default G6 unified interactive Features outline renders Related Indicators and Related Components sections.
	_ = d.Frame()
	tm := a.trees.featuresOutlineState().model
	if !tm.Select("featsec.domain.related.indicators") {
		t.Fatalf("unified Features outline missing featsec.domain.related.indicators on page:panes")
	}
	if !tm.Select("featsec.domain.related.components") {
		t.Fatalf("unified Features outline missing featsec.domain.related.components on page:panes")
	}

	// Opt-out setting features.domainNav=false hides Domain nav and related Domain sections.
	a.cfg.userSettings[SetFeaturesDomainNav] = false
	a.reloadSettings()
	a.applySettings()
	snapOff := a.collectFeaturesSnapshot()
	navOff, validOff := a.buildFeaturesNav(snapOff, a.featuresCtrl().activeItemKey())
	if slices.ContainsFunc(navOff, func(g views.FeaturesNavGroup) bool { return g.Key == featuresDomainNavGroup }) || validOff["domain:everything"] {
		t.Fatalf("features.domainNav=false did not hide Domain nav group")
	}
	panesOff := a.buildFeaturesDetail(snapOff, "page:"+SpacePanes)
	if len(panesOff.DomainGroups) != 0 {
		t.Fatalf("features.domainNav=false left Related Domain sections on page:panes: %+v", panesOff.DomainGroups)
	}
}

func TestFeaturesDomainLiveASTScannerGroundsRealRepoLocations(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := newFeaturesTestApp(t, Options{})
	a.cfg.userSettings[SetSelfHostedFeaturesEnabled] = SelfHostedFeaturesOn
	a.cfg.userSettings[SetSelfHostedFeaturesRepoPath] = repoRoot
	a.reloadSettings()
	a.applySettings()

	groups := a.collectDomainGroups(repoRoot)
	byKey := map[string]views.DomainGroup{}
	for _, g := range groups {
		byKey[g.Key] = g
	}
	indGroup := byKey[DomainIndicatorsKey]
	foundStatus := false
	for _, it := range indGroup.Items {
		if it.Location.Symbol == "StatusIndicator" && it.Location.File == "views/style.go" {
			foundStatus = true
			if it.Location.StartLine <= 0 || it.Location.EndLine <= it.Location.StartLine {
				t.Fatalf("AST scanner did not resolve real line range for StatusIndicator: %+v", it.Location)
			}
		}
	}
	if !foundStatus {
		t.Fatalf("StatusIndicator missing from scanned Indicators group")
	}
	// Live scanner should also discover Mode* constants and *View builders beyond static entries.
	if len(byKey[DomainModesKey].Items) < 5 {
		t.Fatalf("expected live AST scanner to discover Mode* constants, got %d items", len(byKey[DomainModesKey].Items))
	}
	if len(byKey[DomainViewsKey].Items) < 10 {
		t.Fatalf("expected live AST scanner to discover *View builders, got %d items", len(byKey[DomainViewsKey].Items))
	}
}

func TestFeaturesViewInCodeOpensVimCodeViewerFromUnifiedOutlineAndLegacyView(t *testing.T) {
	fs, readFile := matomeruCheckoutWithCodeFS("/home/dev", "/home/dev/proj/matomeru")
	a, d := newFeaturesTestApp(t, Options{FS: fs, ReadFile: readFile})

	d.Keys("4")
	d.Send(core.CommandEvent{Command: core.Command{
		ID: CmdFeaturesNavSelect, Args: map[string]any{"key": "domain:indicators"}, Actor: User,
	}})
	_ = d.Frame()

	targetID := "features.domain.indicators.ind.status.icon"

	// Part A: Default G6 unified interactive Features outline (`features.outline=true`).
	tm := a.trees.featuresOutlineState().model
	if !tm.Select(targetID) {
		t.Fatalf("unified Features outline missing domain occurrence %q", targetID)
	}
	a.Shell.State = withFocus(a.State(), featuresOutlineTree)
	_ = d.Frame()

	// 1. Pressing enter on a Domain occurrence in the unified outline opens the REAL Vim Code Viewer.
	d.Keys("enter")
	cv := a.activeCodeViewer()
	if a.Modal() != codeViewerModalID || a.Mode() != ModeCodeView || cv == nil {
		t.Fatalf("enter in unified outline did not open Vim code viewer: modal=%q mode=%q status=%q", a.Modal(), a.Mode(), a.Status())
	}
	if cv.state.Title != "Declarative Status Icon" || cv.state.CursorLine != 193 {
		t.Fatalf("unexpected Vim code viewer state: title=%q cursor=%d", cv.state.Title, cv.state.CursorLine)
	}
	if len(cv.state.Highlights) == 0 || cv.state.Highlights[0].StartLine != 193 || cv.state.Highlights[0].EndLine != 219 {
		t.Fatalf("expected source highlight 193..219, got %+v", cv.state.Highlights)
	}
	// Vim motions work inside the viewer.
	d.Keys("j")
	if cv.state.CursorLine != 194 {
		t.Fatalf("j motion in Vim code viewer got line %d, want 194", cv.state.CursorLine)
	}
	// ctrl+o returns cleanly to the exact Features entry, outline tree focus, and selected row.
	d.Keys("ctrl+o")
	if a.Modal() != "" || a.Mode() != ModeNormal || a.Space() != SpaceFeatures || a.Focus() != featuresOutlineTree {
		t.Fatalf("ctrl+o did not return cleanly: modal=%q mode=%q space=%q focus=%q", a.Modal(), a.Mode(), a.Space(), a.Focus())
	}
	if a.featuresCtrl().activeItemKey() != "domain:indicators" || a.trees.featuresOutlineState().model.SelectedID != targetID {
		t.Fatalf("ctrl+o lost Features state: activeKey=%q selected=%q", a.featuresCtrl().activeItemKey(), a.trees.featuresOutlineState().model.SelectedID)
	}

	// 2. Dedicated unmodified key C opens Vim Code Viewer, and backspace returns cleanly.
	d.Keys("C")
	if a.Modal() != codeViewerModalID || a.Mode() != ModeCodeView {
		t.Fatalf("C in unified outline did not open Vim code viewer: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	d.Keys("backspace")
	if a.Modal() != "" || a.Mode() != ModeNormal || a.Focus() != featuresOutlineTree {
		t.Fatalf("backspace did not return cleanly: modal=%q mode=%q focus=%q", a.Modal(), a.Mode(), a.Focus())
	}

	// 3. Global g k invokes code.open from focused node facts, and q returns cleanly.
	d.Keys("g", "k")
	if a.Modal() != codeViewerModalID || a.Mode() != ModeCodeView {
		t.Fatalf("g k did not open Vim code viewer: modal=%q mode=%q status=%q", a.Modal(), a.Mode(), a.Status())
	}
	d.Keys("q")
	if a.Modal() != "" || a.Mode() != ModeNormal || a.Focus() != featuresOutlineTree {
		t.Fatalf("q did not close Vim code viewer cleanly: modal=%q mode=%q focus=%q", a.Modal(), a.Mode(), a.Focus())
	}

	// Part B: Legacy non-outline Features view (`features.outline=false`).
	a.cfg.userSettings[SetFeaturesOutline] = false
	a.reloadSettings()
	a.applySettings()
	root := a.View(a.State())
	if root.Find(targetID) == nil {
		t.Fatalf("expected legacy Features view to contain %q", targetID)
	}
	a.Shell.State = withFocus(a.State(), targetID)

	d.Keys("enter")
	if a.Modal() != codeViewerModalID || a.Mode() != ModeCodeView {
		t.Fatalf("enter in legacy view did not open Vim code viewer: modal=%q mode=%q status=%q", a.Modal(), a.Mode(), a.Status())
	}
	d.Keys("ctrl+o")
	if a.Modal() != "" || a.Mode() != ModeNormal || a.Focus() != targetID {
		t.Fatalf("ctrl+o from legacy view did not restore exact focus %q: got %q", targetID, a.Focus())
	}
}
