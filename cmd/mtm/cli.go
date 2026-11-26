package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"ofam.dev/matomeru/app"
	ocli "ofam.dev/ochazuke/cli"
	"ofam.dev/ochazuke/core"
	"ofam.dev/oden"
)

func newCLIApp(g globalOpts, args []string, cancel context.CancelFunc, stdin io.Reader, stdout, stderr io.Writer) *oden.App {
	info := ocli.AppInfo{
		Name:     "mtm",
		Version:  mtmVersion,
		Summary:  "Matomeru v2 — four-space TUI and CLI over the unified store",
		HelpText: usage,
		Modes:    app.Modes,
		Bindings: func() []core.Binding {
			v, err := app.Inspect(app.InspectBindings, configDir())
			if err != nil {
				return app.DefaultBindings()
			}
			bs, _ := v.([]core.Binding)
			return bs
		},
		Specs: app.CommandSpecs,
		RunSmoke: func(out io.Writer, dump bool) error {
			if g.tutorial {
				return app.RunTutorialSmoke(out)
			}
			return app.RunSmoke(out, dump)
		},
		RunTUI: func(_ context.Context, rootArgs []string) error {
			if len(rootArgs) > 0 {
				return fmt.Errorf("unknown flag %s", rootArgs[0])
			}
			if g.tutorial {
				cancel() // the TUI reads ctrl+c itself
				return runTutorial()
			}
			dbPath := g.db
			if dbPath == "" {
				dbPath, _ = defaultDB()
			}
			store, err := openStoreWithSeed(g)
			if err != nil {
				return err
			}
			cancel() // the TUI reads ctrl+c itself
			reload, err := runTUI(store, dbPath, stderr)
			_ = store.Close()
			if err != nil {
				return err
			}
			if reload {
				return execSelfReload()
			}
			return nil
		},
	}

	versionFlag := len(args) > 0 && (args[0] == "--version" || args[0] == "-v")
	a := ocli.NewApp(info, args,
		oden.WithPrefixes("o"),
		oden.WithStdio(stdin, stdout, stderr),
		withCommands(
			mtmVersionCommand(versionFlag),
			mtmBindingCommand(),
			mtmInspectCommand(),
		),
	)

	a.Add(
		mtmAboutCommand(g),
		mtmConfigCommand(),
		mtmRecordCommand(g),
		mtmMCPCommand(g),
		mtmAgentCommand(),
		mtmHookCommand(),
		mtmDaemonCommand(g),
		mtmWebCommand(),
		mtmHarnessCommand(),
		mtmSetupCommand(),
		mtmMigrateCommand(g),
		mtmUndoCommand(g),
		mtmRedoCommand(g),
		mtmTestFormatCommand(),
	)
	a.Add(mtmNounCommands(g)...)

	baseClaims := a.Claims
	a.Claims = func(rootArgs []string) bool {
		if len(rootArgs) > 1 && strings.EqualFold(rootArgs[0], "commands") {
			return true
		}
		return baseClaims != nil && baseClaims(rootArgs)
	}

	baseDefault := a.Default
	a.Default = func(ctx context.Context, rootArgs []string) error {
		if handled, err := dispatchDynamicNoun(ctx, a, g, rootArgs); handled {
			return err
		}
		return baseDefault(ctx, rootArgs)
	}

	return a
}

func withCommands(cmds ...oden.Command) oden.Option {
	return func(a *oden.App) {
		a.Add(cmds...)
	}
}
