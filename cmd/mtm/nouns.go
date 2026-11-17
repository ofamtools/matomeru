package main

import (
	"embed"
	"fmt"
	"os"

	"ofam.dev/matomeru/internal/harness"
	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
)

// mtm's nouns declared as data (spine S1, run plan 5.1): nouns/NOUN.json,
// one file per noun, read by ochazuke's one loader. The files hold the
// props, the store's source, the verbs with the handler each names, the
// key rows and the settings; the Go side registers the handlers and
// sources once, by name (nounHandlers), and nothing else declares these
// nouns. ochazuke.handler lists the names (mtm handler list), and rule
// check handlers fails on a row naming a missing one or one no row uses.
//
//go:embed nouns/*.json
var nounFiles embed.FS

// declareNouns registers the handlers and sources the noun files name,
// then declares the nouns. New calls it before the code that uses the
// nouns; bindNouns loads their key rows later. A problem is reported on
// stderr and in the rule check, and the rest loads (record first).
func (a *App) declareNouns() {
	a.nouns = ocli.ReadNouns(nounFiles, "nouns")
	a.nounHandlers()
	a.reportNounProblems(a.nouns.Declare(a.r))
}

// bindNouns loads the noun files' key rows, once every verb, alias and
// guard they name is registered (where mtm's other default rows load).
func (a *App) bindNouns() { a.reportNounProblems(a.nouns.Bind(a.r)) }

func (a *App) reportNounProblems(problems []ochazuke.DeclProblem) {
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, "mtm: "+p.String())
	}
}

// storeLoads are the fetch verbs that load what noun files' stores answer
// (the daemon's listing read into the cache the pane and session stores
// list from): each declares those nouns as the stores it writes
// (ochazuke Operation.Writes), so once its job is done their watches, an
// index tree's among them, read again, as after a change verb. A verb
// declared in Go before its noun file existed says it here, beside the
// sources, until its own declaration can carry it (panes.refresh: the
// Panes page's files are not edited while that page stays, run plan 5.6).
var storeLoads = map[string][]string{"panes.refresh": {"pane", "session"}}

// declareStoreLoads writes storeLoads onto the verbs, once they are
// declared.
func (a *App) declareStoreLoads() {
	for key, nouns := range storeLoads {
		op, ok := a.r.Operations.Find(ochazuke.Key(a.r.CanonicalKey(key)))
		if !ok {
			continue
		}
		op.Writes = nouns
		a.r.Operations.Set(op)
	}
}

// nounHandlers registers the Go code the noun files cite: each verb row's
// handler under NOUN.VERB, each store's source under NOUN.store.
func (a *App) nounHandlers() {
	r := a.r
	r.AddSource("pane.store", func() ochazuke.Store {
		return a.copyStore("pane", a.paneStore(), a.paneCopyKey, paneCopyRoles.roles(), a.paneRecords)
	})
	r.AddSource("session.store", func() ochazuke.Store {
		return a.copyStore("session", a.sessionStore(), a.sessionCopyKey, sessionCopyRoles.roles(), a.sessionRecords)
	})
	for name, h := range map[string]any{
		"pane.attach":              a.attachPane,
		"pane.preview":             a.previewPane,
		"pane.kill":                a.killPane,
		"pane.killBlocked":         a.killBlocked,
		"pane.activity":            a.paneActivity,
		"pane.transcript":          a.paneTranscript,
		"pane.gone":                a.paneEnded,
		"pane.read-turns":          a.readPaneTurns,
		"pane.hints":               a.startRowHints,
		"pane.view":                a.setPaneView,
		"pane.pick":                a.pickPane,
		"pane.associate":           a.associatePane,
		"pane.choose-move":         a.chooseMove,
		"pane.pick-task":           a.pickPaneTask,
		"pane.side":                a.sidePane,
		"pane.copy":                a.paneCopy,
		"pane.whoami":              a.paneWhoami,
		"pane.progress":            a.paneProgress,
		"pane.turns":               a.focusTurns,
		"pane.refresh-turns":       a.refreshPaneTurns,
		"pane.scroll-turns":        a.scrollTurns,
		"pane.prompt":              a.focusPrompt,
		"pane.settings":            a.showPaneSettings,
		"pane.set-model":           a.setPaneSetting(harness.SettingModel),
		"pane.set-effort":          a.setPaneSetting(harness.SettingEffort),
		"pane.set-fast":            a.setPaneSetting(harness.SettingFast),
		"pane.toggle-fast":         a.togglePaneFast,
		"pane.set-permission-mode": a.setPaneSetting(harness.SettingPermission),
		"pane.settings-line":       a.focusPaneSettings,
		"pane.read-settings":       a.readCodexSettings,
		"pane.new":                 a.newPane,
		"pane.start":               a.startShell,
		"session.attach":           a.attachSession,
		"session.kill":             a.killSession,
		"session.associate":        a.associateSession,
		"session.choose-move":      a.chooseMove,
		"session.events":           a.sessionEvents,
	} {
		r.AddHandler(name, h)
	}
	for _, v := range paneItemsVerbs {
		r.AddHandler("pane."+v.verb, a.runPaneItems(v), ochazuke.WithParams(v.parameters()))
	}
	// The daemon verbs of a noun declared in a file: the file's row is the
	// declaration, the table's row the handler's data and its parameters
	// (defineWireVerbs declares the other nouns' rows itself).
	for _, w := range wireVerbs {
		if a.nouns.Declares(w.Noun) {
			r.AddHandler(w.key(), a.wireHandler(w), ochazuke.WithParams(w.params()))
		}
	}
}
