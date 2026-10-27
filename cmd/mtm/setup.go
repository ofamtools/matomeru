package main

import (
	"os"
	"path/filepath"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/setup"
)

// mtm setup: install, inspect and remove mtm's own turn-capture hooks per
// harness (internal/setup), beside the TUIOS integration. setup list
// reads each item's state, setup plan prints the file each install would
// write in full, without writing; install and uninstall touch only mtm's
// own file or hook entries and ask first (--yes answers). --home DIR
// stands for the home directory, so a dry run or a test writes under DIR
// instead of ~/.claude, ~/.codex and ~/.omp.

// defineSetup adds the setup noun.
func (a *App) defineSetup() {
	a.r.Define("setup").
		Verb("list", a.setupList, "List the capture hooks mtm can install and their state (missing, installed, outdated, conflict)").Fetch().
		Verb("plan", a.setupPlan, "Print what installing writes (path and whole content) without writing").Fetch().
		Verb("install", a.setupInstall, "Install mtm's turn-capture hooks; omit item for all of them").Confirm("Write mtm's turn-capture hooks into the harness configuration?").
		Verb("uninstall", a.setupUninstall, "Remove mtm's turn-capture hooks and nothing else; omit item for all of them").Confirm("Remove mtm's turn-capture hooks?")
	a.ownedBy("setup", "app")
	params := map[string]ochazuke.Parameter{
		"item": {Type: "string", Enum: setup.IDs(), Description: "One item; omit for all"},
		"home": {Type: "string", Description: "A directory standing for the home directory (dry runs, tests)"},
	}
	for _, verb := range []string{"list", "plan", "install", "uninstall"} {
		a.parameters("setup."+verb, params)
		op := a.r.Operations[a.r.CanonicalKey("setup."+verb)]
		op.Positional = []string{"item"}
		a.r.Operations[a.r.CanonicalKey("setup."+verb)] = op
	}
}

// setupOptions are the hooks' binary and database, and where the
// harnesses' files are.
func (a *App) setupOptions(ctx ochazuke.Ctx) (setup.Options, error) {
	bin, err := os.Executable()
	if err != nil {
		return setup.Options{}, err
	}
	data, err := filepath.Abs(a.dataPath)
	if err != nil {
		return setup.Options{}, err
	}
	return setup.Options{Home: ochazuke.StringArg(ctx.Args, "home", ""), Bin: bin, Data: data}, nil
}

// setupEach runs fn for the item argument, else every item.
func (a *App) setupEach(ctx ochazuke.Ctx, fn func(setup.Options, string) (any, error)) ochazuke.Result {
	o, err := a.setupOptions(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	ids := setup.IDs()
	if id := ochazuke.StringArg(ctx.Args, "item", ""); id != "" {
		ids = []string{id}
	}
	out := []any{}
	for _, id := range ids {
		v, err := fn(o, id)
		if err != nil {
			return ochazuke.Failed(err)
		}
		out = append(out, v)
	}
	return ochazuke.Done(out)
}

func (a *App) setupList(ctx ochazuke.Ctx) ochazuke.Result {
	return a.setupEach(ctx, func(o setup.Options, id string) (any, error) { return o.Status(id) })
}

func (a *App) setupPlan(ctx ochazuke.Ctx) ochazuke.Result {
	return a.setupEach(ctx, func(o setup.Options, id string) (any, error) { return o.Plan(id) })
}

func (a *App) setupInstall(ctx ochazuke.Ctx) ochazuke.Result {
	return a.setupEach(ctx, func(o setup.Options, id string) (any, error) { return o.Install(id) })
}

func (a *App) setupUninstall(ctx ochazuke.Ctx) ochazuke.Result {
	return a.setupEach(ctx, func(o setup.Options, id string) (any, error) { return o.Uninstall(id) })
}
