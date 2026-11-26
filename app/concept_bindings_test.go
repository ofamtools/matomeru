package app

import (
	"testing"

	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/traits"
)

func TestBindingsFeaturesSuggestionsPreserveExistingKeys(t *testing.T) {
	a, _ := newFeaturesTestApp(t, Options{FS: matomeruCheckoutFS("/home/dev", "/home/dev/proj/matomeru")})
	facts := map[string]any{
		"featuresPageActive":     true,
		"featuresCanSuggestProp": true,
		"featuresCanSuggestView": true,
		factSelectTarget:         true, // the index draws a list
	}
	for _, tc := range []struct {
		scope string
		key   string
		want  string
	}{
		{views.ScopeFeaturesContent, "c", CmdFeaturesCaptureFocus},
		{views.ScopeIndex, "v", traits.SelectCmdVisualToggle},
		{views.ScopeIndex, "p", ""},
	} {
		t.Run(tc.scope+"/"+tc.key, func(t *testing.T) {
			result := a.bind.Keymap.Resolve(core.KeyQuery{
				Mode:  ModeNormal,
				Key:   tc.key,
				Chain: []core.ContextFrame{{Scope: tc.scope, Facts: facts}},
			})
			var command string
			if result.Command != nil {
				command = result.Command.ID
			}
			if command != tc.want {
				t.Fatalf("resolved %q, want %q", command, tc.want)
			}
		})
	}
}
