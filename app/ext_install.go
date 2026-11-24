package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/ext"
	"ofam.dev/matomeru/runs"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/toast"
)

type loadedExtCatalog struct {
	binary   string
	manifest ext.Manifest
}

func (a *App) showExtensionCatalog() bool {
	if a == nil || a.cfg == nil || a.cfg.Settings == nil {
		return true
	}
	return a.cfg.Settings.Bool(SetExtensionsShowCatalog, true)
}

func (a *App) extensionGoPrivate() string {
	if a != nil && a.cfg != nil && a.cfg.userSettings != nil {
		if _, set := a.cfg.userSettings[SetExtensionsGoPrivate]; set {
			return strings.TrimSpace(a.cfg.Settings.String(SetExtensionsGoPrivate, ext.DefaultGoPrivate))
		}
	}
	if a != nil && a.ext != nil && a.ext.Extensions != nil {
		if gp := strings.TrimSpace(a.ext.Extensions.GoPrivate()); gp != "" {
			return gp
		}
	}
	if a != nil && a.cfg != nil && a.cfg.Settings != nil {
		return strings.TrimSpace(a.cfg.Settings.String(SetExtensionsGoPrivate, ext.DefaultGoPrivate))
	}
	return ext.DefaultGoPrivate
}

func (a *App) newLiveExtManager() *ext.Manager {
	return ext.New(ext.Options{
		IncludeCatalog: a.showExtensionCatalog(),
		GoPrivate:      a.extensionGoPrivate(),
	})
}

func (a *App) syncLiveExtManager() {
	if a.opts.Extensions == nil && a.ext.Extensions != nil {
		a.ext.Extensions.SetIncludeCatalog(a.showExtensionCatalog())
	}
}

func (a *App) extensionDirs() []string {
	dirs := append(slices.Clone(a.opts.ExtensionDirs), a.cfg.Settings.Strings("extensionDirs", nil)...)
	for i, d := range dirs {
		dirs[i] = expandHome(d)
	}
	return dirs
}

func (a *App) filterExtensionItems(kind string, items []data.Record) []data.Record {
	if kind != data.KindExtension || a.showExtensionCatalog() {
		return items
	}
	out := make([]data.Record, 0, len(items))
	for _, rec := range items {
		if rec.Text("runStatus") == ext.StatusAvailable {
			continue
		}
		out = append(out, rec)
	}
	return out
}

func (a *App) applyExtensionFacts(rec data.Record, facts map[string]any) {
	if rec.Kind != data.KindExtension || facts == nil {
		return
	}
	facts["extAvailable"] = rec.Text("runStatus") == ext.StatusAvailable
	_, inCatalog := ext.LookupCatalog(rec.Text("name"))
	facts["extInstallable"] = strings.TrimSpace(rec.Text("installPkg")) != "" || inCatalog
}

func rediscoverExtensions(ctx context.Context, mgr *ext.Manager, dirs []string, store *data.Store) ([]ext.Result, []loadedExtCatalog, error) {
	res, err := mgr.Discover(ctx, dirs, store)
	var catalogs []loadedExtCatalog
	for _, r := range res {
		if r.Status == ext.StatusHealthy && r.Binary != "" {
			if m, cerr := ext.LoadCatalog(ctx, r.Binary); cerr == nil && len(m.Schemas) > 0 {
				catalogs = append(catalogs, loadedExtCatalog{binary: r.Binary, manifest: m})
			}
		}
	}
	return res, catalogs, err
}

func (a *App) mountLoadedCatalogs(catalogs []loadedExtCatalog) int {
	mounted := 0
	for _, lc := range catalogs {
		if merr := a.MountCatalog(lc.binary, lc.manifest); merr == nil {
			mounted += len(lc.manifest.Schemas)
		}
	}
	return mounted
}

func formatExtRefreshStatus(res []ext.Result, mounted int) string {
	found, healthy, available := 0, 0, 0
	for _, r := range res {
		if r.Binary != "" {
			found++
		}
		if r.Status == ext.StatusHealthy {
			healthy++
		}
		if r.Status == ext.StatusAvailable {
			available++
		}
	}
	base := fmt.Sprintf("%d of %d extensions found, %d healthy", found, len(res), healthy)
	if available > 0 {
		base += fmt.Sprintf(", %d available", available)
	}
	if mounted > 0 {
		base += fmt.Sprintf(" · %d external noun(s) mounted", mounted)
	}
	return base
}

func runExitCode(rec *data.Record) (int, bool) {
	if rec == nil {
		return 0, false
	}
	v, ok := rec.Value("exitCode")
	if !ok || v == nil {
		return 0, false
	}
	switch x := v.(type) {
	case int:
		return x, true
	case int64:
		return int(x), true
	case float64:
		return int(x), true
	default:
		n, err := strconv.Atoi(strings.TrimSpace(data.Text(v)))
		return n, err == nil
	}
}

func runSucceeded(rec *data.Record) bool {
	if rec == nil || rec.Text("runStatus") != runs.StatusDone {
		return false
	}
	code, ok := runExitCode(rec)
	return !ok || code == 0
}

func (a *App) extInstall(s core.State, c core.Command) core.StepResult {
	ref, ok := a.subject(s, c)
	if !ok || ref.Kind != data.KindExtension {
		a.say("no extension here to install")
		return core.Next(s)
	}
	rec, err := a.Store.Get(ref)
	if err != nil {
		a.fail("install extension", err)
		return core.Next(s)
	}
	name := strings.TrimSpace(rec.Text("name"))
	pkg := strings.TrimSpace(rec.Text("installPkg"))
	if pkg == "" {
		if entry, found := ext.LookupCatalog(name); found {
			pkg = entry.Package
		}
	}
	if pkg == "" {
		a.say("%s has no install package", name)
		return core.Next(s)
	}
	if a.ext.Extensions == nil || a.rt.Runner == nil {
		a.say("extensions are not live in this build")
		return core.Next(s)
	}
	a.syncLiveExtManager()
	mgr, runner, store := a.ext.Extensions, a.rt.Runner, a.Store
	dirs := a.extensionDirs()
	goPrivate := a.extensionGoPrivate()

	a.say("installing %s… (3 shows output)", name)
	a.pushToast("Installing "+name+"…", toast.Info)
	a.background(func(ctx context.Context) func(core.State) core.State {
		runRec, err := mgr.InstallWithGoPrivate(ctx, name, goPrivate, runner)
		if err != nil {
			return func(s core.State) core.State {
				a.fail("install "+name, err)
				a.pushToast("install "+name+" failed · press 3 for output", toast.Err)
				return s
			}
		}
		if done := runner.Done(runRec.ID); done != nil {
			select {
			case <-done:
			case <-ctx.Done():
				return nil
			}
		}
		finalRun, _ := store.Get(runRec.Ref())
		if !runSucceeded(finalRun) {
			return func(s core.State) core.State {
				msg := fmt.Sprintf("install %s failed · press 3 for output", name)
				a.say("%s", msg)
				a.pushToast(msg, toast.Err)
				return s
			}
		}
		_, catalogs, derr := rediscoverExtensions(ctx, mgr, dirs, store)
		return func(s core.State) core.State {
			a.mountLoadedCatalogs(catalogs)
			a.nav.recCacheKeys = map[string]string{}
			a.viewMemo.gen++
			msg := fmt.Sprintf("installed %s · ready", name)
			if derr != nil {
				a.say("%s · rediscover warning: %v", msg, derr)
			} else {
				a.say("%s", msg)
			}
			a.pushToast(msg, toast.Ok)
			return s
		}
	})
	return core.Next(s)
}

func (a *App) extInstallAll(s core.State, _ core.Command) core.StepResult {
	if a.ext.Extensions == nil || a.rt.Runner == nil {
		a.say("extensions are not live in this build")
		return core.Next(s)
	}
	a.syncLiveExtManager()
	mgr, runner, store := a.ext.Extensions, a.rt.Runner, a.Store
	var missing []ext.CatalogEntry
	for _, entry := range mgr.Catalog() {
		if entry.Category != ext.CategoryCore {
			continue
		}
		if rec, err := store.GetByField(data.KindExtension, "name", entry.Name); err == nil && rec != nil && rec.Text("runStatus") == ext.StatusHealthy {
			continue
		}
		missing = append(missing, entry)
	}
	if len(missing) == 0 {
		msg := "all core OFAM extensions are already installed"
		a.say("%s", msg)
		a.pushToast(msg, toast.Info)
		return core.Next(s)
	}

	dirs := a.extensionDirs()
	goPrivate := a.extensionGoPrivate()
	a.say("installing %d core OFAM extension(s)… (3 shows output)", len(missing))
	a.pushToast(fmt.Sprintf("Installing %d core OFAM extensions…", len(missing)), toast.Info)
	a.background(func(ctx context.Context) func(core.State) core.State {
		installed := 0
		for _, entry := range missing {
			if ctx.Err() != nil {
				break
			}
			runRec, err := mgr.InstallWithGoPrivate(ctx, entry.Name, goPrivate, runner)
			if err != nil || runRec == nil {
				continue
			}
			if done := runner.Done(runRec.ID); done != nil {
				select {
				case <-done:
				case <-ctx.Done():
					return nil
				}
			}
			if finalRun, err := store.Get(runRec.Ref()); err == nil && runSucceeded(finalRun) {
				installed++
			}
		}
		_, catalogs, _ := rediscoverExtensions(ctx, mgr, dirs, store)
		return func(s core.State) core.State {
			a.mountLoadedCatalogs(catalogs)
			a.nav.recCacheKeys = map[string]string{}
			a.viewMemo.gen++
			msg := fmt.Sprintf("installed %d of %d core OFAM extensions", installed, len(missing))
			a.say("%s", msg)
			tone := toast.Ok
			if installed == 0 {
				tone = toast.Err
			} else if installed < len(missing) {
				tone = toast.Warn
			}
			a.pushToast(msg, tone)
			return s
		}
	})
	return core.Next(s)
}

// --- CLI helpers for `mtm extension catalog` and `mtm extension install` --------

// ExtCatalogItem is one entry returned by CatalogExtensions.
type ExtCatalogItem struct {
	Name     string `json:"name"`
	Category string `json:"category"`
	Package  string `json:"package"`
	Summary  string `json:"summary"`
	Status   string `json:"status"`
	Binary   string `json:"binary,omitempty"`
	Version  string `json:"version,omitempty"`
}

// CatalogExtensions probes the 20 published OFAM catalog tools against dirs and
// PATH, updating store records and returning each tool's status and metadata.
func CatalogExtensions(ctx context.Context, store *data.Store, dirs []string) ([]ExtCatalogItem, error) {
	entries := ext.CatalogEntries()
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name
	}
	mgr := ext.New(ext.Options{Tools: names, Catalog: entries, IncludeCatalog: true})
	results, err := mgr.Discover(ctx, dirs, store)
	byName := make(map[string]ext.Result, len(results))
	for _, r := range results {
		byName[r.Name] = r
	}
	items := make([]ExtCatalogItem, 0, len(entries))
	for _, e := range entries {
		item := ExtCatalogItem{
			Name:     e.Name,
			Category: e.Category,
			Package:  e.Package,
			Summary:  e.Summary,
			Status:   ext.StatusAvailable,
		}
		if r, ok := byName[e.Name]; ok {
			item.Status = r.Status
			item.Binary = r.Binary
			item.Version = r.Version
			if r.Summary != "" {
				item.Summary = r.Summary
			}
		}
		items = append(items, item)
	}
	return items, err
}

// ExtInstallOptions configures InstallExtensions.
type ExtInstallOptions struct {
	Names     []string
	All       bool
	Optional  bool
	GoPrivate string
	GoBinary  string
	Dirs      []string
	Actor     core.Actor
}

// ExtInstallResult is the outcome of installing one OFAM extension via CLI.
type ExtInstallResult struct {
	Name     string `json:"name"`
	Package  string `json:"package,omitempty"`
	Status   string `json:"status"`
	RunID    string `json:"runId,omitempty"`
	ExitCode int    `json:"exitCode"`
	Output   string `json:"output,omitempty"`
	Binary   string `json:"binary,omitempty"`
	Version  string `json:"version,omitempty"`
	Error    string `json:"error,omitempty"`
}

// InstallExtensions installs one or more named extensions (or all missing core
// — and optionally optional — catalog tools) through runs.Runner, waits for
// completion, re-discovers installed binaries, and returns per-tool outcomes.
func InstallExtensions(ctx context.Context, store *data.Store, opts ExtInstallOptions) ([]ExtInstallResult, error) {
	if !opts.All && len(opts.Names) == 0 {
		return nil, errors.New("specify an extension name or --all")
	}
	goPrivate := strings.TrimSpace(opts.GoPrivate)
	if goPrivate == "" {
		goPrivate = ext.DefaultGoPrivate
	}
	mgr := ext.New(ext.Options{
		IncludeCatalog: true,
		GoBinary:       opts.GoBinary,
		GoPrivate:      goPrivate,
		Actor:          opts.Actor,
	})
	runner := runs.New(store, runs.Options{})
	defer runner.Close()

	targets := slices.Clone(opts.Names)
	if opts.All {
		entries := ext.CatalogEntries()
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name
		}
		probeMgr := ext.New(ext.Options{Tools: names, Catalog: entries, IncludeCatalog: true, Actor: opts.Actor})
		discovered, _ := probeMgr.Discover(ctx, opts.Dirs, store)
		healthy := map[string]bool{}
		for _, r := range discovered {
			if r.Status == ext.StatusHealthy {
				healthy[r.Name] = true
			}
		}
		targets = targets[:0]
		for _, e := range entries {
			if e.Category == ext.CategoryOptional && !opts.Optional {
				continue
			}
			if healthy[e.Name] {
				continue
			}
			targets = append(targets, e.Name)
		}
	}

	out := make([]ExtInstallResult, 0, len(targets))
	var errs []error
	var installedNames []string
	for _, name := range targets {
		res := ExtInstallResult{Name: name}
		if entry, ok := ext.LookupCatalog(name); ok {
			res.Package = entry.Package
		} else if rec, err := store.GetByField(data.KindExtension, "name", name); err == nil && rec != nil {
			res.Package = rec.Text("installPkg")
		}
		runRec, err := mgr.InstallWithGoPrivate(ctx, name, goPrivate, runner)
		if err != nil {
			res.Status = "failed"
			res.ExitCode = -1
			res.Error = err.Error()
			out = append(out, res)
			errs = append(errs, err)
			continue
		}
		res.RunID = runRec.ID
		if done := runner.Done(runRec.ID); done != nil {
			select {
			case <-done:
			case <-ctx.Done():
				_ = runner.Cancel(runRec.ID)
				<-done
				return out, ctx.Err()
			}
		}
		finalRun, gerr := store.Get(runRec.Ref())
		if gerr != nil {
			res.Status = "failed"
			res.ExitCode = -1
			res.Error = gerr.Error()
			out = append(out, res)
			errs = append(errs, gerr)
			continue
		}
		res.Output = strings.TrimSpace(finalRun.Text("output"))
		if code, ok := runExitCode(finalRun); ok {
			res.ExitCode = code
		}
		if runSucceeded(finalRun) {
			res.Status = "installed"
			installedNames = append(installedNames, name)
		} else {
			res.Status = "failed"
			res.Error = fmt.Sprintf("install %s exited with status %d", name, res.ExitCode)
			errs = append(errs, errors.New(res.Error))
		}
		out = append(out, res)
	}

	if len(installedNames) > 0 {
		rediscoverMgr := ext.New(ext.Options{Tools: installedNames, IncludeCatalog: true, Actor: opts.Actor})
		if disc, _ := rediscoverMgr.Discover(ctx, opts.Dirs, store); len(disc) > 0 {
			byName := make(map[string]ext.Result, len(disc))
			for _, r := range disc {
				byName[r.Name] = r
			}
			for i := range out {
				if r, ok := byName[out[i].Name]; ok {
					out[i].Binary = r.Binary
					out[i].Version = r.Version
				}
			}
		}
	}
	return out, errors.Join(errs...)
}
