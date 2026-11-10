package main

import (
	"runtime"
	"slices"
	"time"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/contextitems"
	"ofam.dev/matomeru/internal/daemon"
)

// How extraction checks the paths it finds in text. Each check is a stat
// on this machine, so:
//   - a path of a pane on another host (its session is host-qualified,
//     daemon.SplitIdentity) is never looked at here: the same name is
//     another file, so the item keeps its path and its exists stays
//     unknown;
//   - a path under a skip root (settingContextStatSkip) is not looked at
//     either: on macOS a lookup under /home or /net asks the automounter
//     and the directory service, which a pane that prints such paths every
//     second made busy;
//   - every other answer is remembered for contextStatTTL (at most
//     contextStatMax paths), so a screen extracted again costs no system
//     calls for the paths it printed a moment ago. A file created within
//     the TTL shows as missing until it expires.
const (
	settingContextStatSkip = "context.stat-skip-roots"
	contextStatTTL         = 30 * time.Second
	contextStatMax         = 4096
)

// contextStatSkipDefault is the autofs roots of macOS; elsewhere none.
func contextStatSkipDefault() []string {
	if runtime.GOOS == "darwin" {
		return []string{"/home", "/net", "/Network"}
	}
	return []string{}
}

var contextStatSkipSetting = ochazuke.Setting{
	Name: settingContextStatSkip, Type: ochazuke.SettingList, Default: contextStatSkipDefault(),
	Description: "Folders whose contents extraction does not check on disk (a path under one keeps its text and an unknown exists): automounted roots, where checking wakes the automounter. [] checks every path",
}

// observeContextStat makes the path checker and keeps it in step with the
// setting.
func (a *App) observeContextStat() {
	settings := a.r.Settings()
	set := func(value []string) {
		a.cx.stats.Store(contextitems.NewStatter(contextStatTTL, contextStatMax, value))
	}
	set(settings.Strings(settingContextStatSkip))
	settings.Observe(settingContextStatSkip, func(_ string, value any) { set(slices.Clone(value.([]string))) })
}

// extractItems is contextitems.Extract with the paths checked as above;
// safe from any goroutine.
func (a *App) extractItems(text string, width int, source contextitems.Source) []contextitems.Item {
	return contextitems.ExtractStat(text, width, source, a.statFor(source))
}

func (a *App) statFor(source contextitems.Source) contextitems.StatFunc {
	if host, _, _ := daemon.SplitIdentity(source.Session); host != "" {
		return contextitems.Remote()
	}
	if s := a.cx.stats.Load(); s != nil {
		return s.Local()
	}
	return nil
}
