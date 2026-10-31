package main

import (
	"fmt"
	"slices"

	"ofam.dev/matomeru/internal/contextitems"
	"ofam.dev/ochazuke"
)

const settingContextExtractFrom = "context.extract-from"

var contextExtractSetting = ochazuke.Setting{
	Name: settingContextExtractFrom, Type: ochazuke.SettingList, Default: []string{"prompt", "response"},
	Description: "Sources that extract context: prompt, response, scrollback, transcript, command. Explicit manual/MCP adds always run; [] disables automatic extraction",
	Validate: func(value any) error {
		for _, origin := range value.([]string) {
			if !slices.Contains(contextitems.Origins, origin) || origin == "unknown" {
				return fmt.Errorf("unknown context origin %q", origin)
			}
		}
		return nil
	},
}

// contextExtraction is an immutable setting snapshot. Settings themselves
// belong to the loop; the journal reader only reads this atomic value.
func (a *App) contextExtraction() []string {
	if value := a.cx.extractFrom.Load(); value != nil {
		return value.([]string)
	}
	return []string{"prompt", "response"}
}

func extractsFrom(origins []string, source contextitems.Source) bool {
	origin := source.Origin()
	return origin == "manual" || origin == "mcp" || slices.Contains(origins, origin)
}

func (a *App) observeContextExtraction() {
	settings := a.r.Settings()
	a.cx.extractFrom.Store(settings.Strings(settingContextExtractFrom))
	settings.Observe(settingContextExtractFrom, func(_ string, value any) {
		a.cx.extractFrom.Store(slices.Clone(value.([]string)))
	})
}
