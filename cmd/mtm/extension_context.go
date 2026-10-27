package main

import (
	"path/filepath"
	"slices"
	"time"

	"github.com/unixsuperhero/ochazuke"
	ocli "github.com/unixsuperhero/ochazuke/cli"
	"github.com/unixsuperhero/ochazuke/markdown/cliview"
)

// What mtm hands its extensions besides their verbs' arguments.
//
// Project context: every exec has MTM_PROJECT_DIRS, the context project's
// directories (os.PathListSeparator between; empty without one; the
// context project is the focus stack's, else the pin, extension_scope.go)
// with MTM_PROJECT and MTM_PROJECT_LABEL, so a
// tool can scope what it lists to the work at hand (o-md's doc list adds
// each directory's top-level *.md and docs/**/*.md). The environment is
// read on the loop: a verb's when it is run, a page's loads when the
// extension's rows reload (reloadExtension), kept for the jobs that run
// off the loop.
//
// Detail kinds: mtm registers ochazuke's markdown kind (markdown/cliview),
// so a page whose detail is {kind: markdown, pathField: F} draws the
// selected row's file as the inline Markdown editor, committed on change.
//
// Noun parameters: a verb parameter naming one of mtm's nouns that mtm
// can choose (pane) and missing from the call opens mtm's chooser for it
// in the TUI, then runs the verb with the choice (o-md's doc send-notes
// asks for its pane so).

// projectDirsEnv names the pinned project's directories for an extension.
const projectDirsEnv = "MTM_PROJECT_DIRS"

// registerDetailKinds registers the detail kinds mtm draws.
func registerDetailKinds() error { return cliview.Register() }

// projectDirs are the context project's directories (contextProject,
// extension_scope.go), its default first.
func (a *App) projectDirs() []string { return a.projectDirsOf(a.contextProject()) }

// projectDirsOf are a project's directories, its default first.
func (a *App) projectDirsOf(id string) []string {
	if id == "" {
		return nil
	}
	p, ok := a.project(id)
	if !ok {
		return nil
	}
	var out []string
	if p.DefaultTree != "" {
		out = append(out, p.DefaultTree)
	}
	for _, t := range p.Trees {
		if path := filepath.Clean(t.Path); !slices.Contains(out, path) {
			out = append(out, path)
		}
	}
	return out
}

// syncFetchEnv keeps the environment the page loads of x run with: read
// here, on the loop.
func (a *App) syncFetchEnv(x *extension) {
	env := a.extEnv(x.name)
	x.fetchEnv.Store(&env)
}

// loadEnv is the environment of x's page loads (off the loop).
func (x *extension) loadEnv(fallback []string) []string {
	if env := x.fetchEnv.Load(); env != nil {
		return *env
	}
	return fallback
}

// nounChoosers are the host nouns mtm asks a missing parameter of.
var nounChoosers = map[string]bool{"pane": true}

// missingNounParam is the first parameter (by name) of an extension verb
// that names a noun mtm can choose and that the call did not set.
func missingNounParam(doc ocli.CatalogDoc, noun, verb string, args ochazuke.Arguments) (string, ochazuke.Parameter, bool) {
	for _, n := range doc.Nouns {
		if n.Name != noun {
			continue
		}
		for _, v := range n.Verbs {
			if v.Name != verb {
				continue
			}
			names := make([]string, 0, len(v.Params))
			for name := range v.Params {
				names = append(names, name)
			}
			slices.Sort(names)
			for _, name := range names {
				p := v.Params[name]
				if !nounChoosers[p.Noun] {
					continue
				}
				if s, ok := args[name].(string); !ok || s == "" {
					return name, p, true
				}
			}
		}
	}
	return "", ochazuke.Parameter{}, false
}

// askNounParam opens mtm's chooser of the parameter's noun; the choice
// runs the verb with it.
func (a *App) askNounParam(x *extension, noun, verb, target, name string, p ochazuke.Parameter, args ochazuke.Arguments) ochazuke.Result {
	title := noun + " " + verb + ": choose a " + p.Noun
	_, err := a.r.OpenChooser(ochazuke.ChooserSpec{
		Kind:   "ext-" + p.Noun,
		Title:  title,
		Rows:   a.panePickerRows(time.Now()),
		Empty:  "no panes",
		Keep:   []int{0},
		Filter: panePickerFilter,
		Choose: func(row ochazuke.ChooserRow) ochazuke.Result {
			chosen, _ := row.Data.(pane)
			next := ochazuke.Arguments{}
			for k, v := range args {
				next[k] = v
			}
			next[name] = chosen.ID
			return a.extRun(x, noun, verb, target, next, nil)
		},
	})
	return resultError(err)
}
