package views

import (
	"testing"
)

func TestCodeLocationFormatAndValid(t *testing.T) {
	cases := []struct {
		loc   CodeLocation
		valid bool
		want  string
	}{
		{CodeLocation{}, false, ""},
		{CodeLocation{File: "views/style.go"}, true, "views/style.go"},
		{CodeLocation{File: "views/style.go", StartLine: 193}, true, "views/style.go:193"},
		{CodeLocation{File: "views/style.go", StartLine: 193, EndLine: 193}, true, "views/style.go:193"},
		{CodeLocation{File: "views/style.go", StartLine: 193, EndLine: 219}, true, "views/style.go:193-219"},
	}
	for _, tc := range cases {
		if tc.loc.Valid() != tc.valid {
			t.Errorf("Valid(%+v) = %v, want %v", tc.loc, tc.loc.Valid(), tc.valid)
		}
		if got := tc.loc.Format(); got != tc.want {
			t.Errorf("Format(%+v) = %q, want %q", tc.loc, got, tc.want)
		}
	}
}

func TestFeaturesPageViewDomainGroupsAndCodeLocationFacts(t *testing.T) {
	root := FeaturesPageView(FeaturesPageSpec{
		ActiveKey: "domain:everything",
		NavGroups: []FeaturesNavGroup{
			{
				Key:   "domain",
				Title: "Domain",
				Items: []FeaturesNavItem{
					{Key: "domain:everything", Section: "domain", Label: "Everything", Icon: "❖", Count: 2, Active: true},
					{Key: "domain:indicators", Section: "domain", Label: "Indicators", Icon: "●", Count: 1},
					{Key: "domain:components", Section: "domain", Label: "Components", Icon: "◫", Count: 1},
				},
			},
		},
		Detail: FeaturesDetailSpec{
			Kind:     "domain",
			Key:      "domain:everything",
			Title:    "Everything",
			Subtitle: "Domain · Complete codebase index",
			DomainGroups: []DomainGroup{
				{
					Key:     "indicators",
					Title:   "Indicators",
					Icon:    "●",
					Summary: "First-class visual status indicators",
					Items: []DomainOccurrence{
						{
							ID:             "ind.status.icon",
							Classification: "indicators",
							Name:           "Declarative Status Icon",
							Glyph:          "●",
							Tone:           "accent",
							Summary:        "Schema-driven status glyph",
							Location:       CodeLocation{File: "views/style.go", StartLine: 193, EndLine: 219, Symbol: "StatusIndicator"},
							Tags:           []string{"indicator", "status"},
						},
					},
				},
				{
					Key:   "components",
					Title: "Components",
					Icon:  "◫",
					Items: []DomainOccurrence{
						{
							ID:             "comp.tree",
							Classification: "components",
							Name:           "Hierarchical Tree",
							Glyph:          "↳",
							Tone:           "info",
							Location:       CodeLocation{File: "views/tree.go", StartLine: 338, EndLine: 437, Symbol: "TreeViewWith"},
						},
					},
				},
			},
		},
	})

	indRow := root.Find("features.domain.indicators.ind.status.icon")
	if indRow == nil || !indRow.Focusable || indRow.Scope != ScopeFeaturesContent {
		t.Fatalf("unexpected indicator row: %+v", indRow)
	}
	if indRow.Facts["hasCodeLocation"] != true ||
		indRow.Facts["codeFile"] != "views/style.go" ||
		indRow.Facts["codeStartLine"] != 193 ||
		indRow.Facts["codeEndLine"] != 219 ||
		indRow.Facts["codeSymbol"] != "StatusIndicator" ||
		indRow.Facts["domainClassification"] != "indicators" {
		t.Fatalf("unexpected indicator row facts: %+v", indRow.Facts)
	}

	compRow := root.Find("features.domain.components.comp.tree")
	if compRow == nil || compRow.Facts["codeFile"] != "views/tree.go" {
		t.Fatalf("unexpected component row: %+v", compRow)
	}

	rendered := frame(t, root, "features.domain.indicators.ind.status.icon", 140, 36)
	wantText(t, rendered,
		"Domain",
		"Everything",
		"Indicators",
		"Components",
		"Declarative Status Icon",
		"views/style.go:193-219",
		"StatusIndicator",
		"Hierarchical Tree",
		"views/tree.go:338-437",
	)
}
