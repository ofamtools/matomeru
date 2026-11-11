package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ofam.dev/matomeru/store"
	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
)

// mtm as host: other family tools (o-ai, o-qbt, ...) stay separate bins and
// show their pages as tabs after mtm's own. The registry is data, one JSON
// file per extension under <data>/extensions/<name>.json holding the bin
// and the catalog it answered (`<bin> catalog`); mounting reads these
// files and never execs, so the CLI starts as fast as before. Register and
// refresh are the only writers. nabe keeps its own registry and code; this
// is mtm's.
//
// Each extension's data pages (catalog pages with lists) are mounted with
// ochazuke's cli.MountPage; a catalog without one gets a page synthesized
// from its application nouns: a list per noun with a row source, a list of
// verbs for a noun nothing lists rows of. Rows are instances of the host
// noun <ext>.<noun> (extensions.noun-names, extension_names.go), whose
// verbs are proxies: `<bin> NOUN VERB [--target
// ID] --json ARGS --yes`, run off the loop after mtm asked the verb's own
// confirm. Rows load from the catalog's state verb (one call), else per
// list, when the page is opened and after each verb of the extension.
//
// Every exec of an extension has MTM_DATA (mtm's data directory) and
// MTM_EXTENSION (its registered name) in its environment: a tool that keeps
// data opens store.OpenExtension(MTM_DATA, MTM_EXTENSION), its own file
// <data>/ext/<name>.db, never mtm's; remove --purge deletes that file.

// extEntry is one registry file.
type extEntry struct {
	Bin          string          `json:"bin"`
	RegisteredAt time.Time       `json:"registeredAt"`
	Catalog      ocli.CatalogDoc `json:"catalog"`
	// BinStamp is the bin's file (links resolved) when Catalog was read:
	// retained for file diagnostics and legacy registry compatibility.
	// Freshness comes from comparing the catalog itself, not this stamp.
	BinStamp *extBinStamp `json:"binStamp,omitempty"`
}

// extRegistry is the directory of registry files.
type extRegistry struct{ Dir string }

// extensionsDir is the registry's directory under mtm's data directory.
const extensionsDir = "extensions"

// extExecTimeout bounds every exec of an extension bin.
const extExecTimeout = 60 * time.Second

// extWaitDelay is how long an exec waits for its output after the
// timeout killed the bin.
const extWaitDelay = time.Second

func (g extRegistry) path(name string) string { return filepath.Join(g.Dir, name+".json") }

// Names lists the registered extensions, sorted.
func (g extRegistry) Names() ([]string, error) {
	files, err := os.ReadDir(g.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, f := range files {
		if name, ok := strings.CutSuffix(f.Name(), ".json"); ok && !f.IsDir() && !strings.HasPrefix(name, ".") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// Read loads one registry file.
func (g extRegistry) Read(name string) (extEntry, error) {
	var e extEntry
	data, err := os.ReadFile(g.path(name))
	if errors.Is(err, os.ErrNotExist) {
		return e, fmt.Errorf("extension %s is not registered", name)
	}
	if err != nil {
		return e, err
	}
	if err := json.Unmarshal(data, &e); err != nil {
		return e, fmt.Errorf("%s: %w", g.path(name), err)
	}
	return e, nil
}

// Ordered lists the registered extensions in tab order: registration
// time, then name.
func (g extRegistry) Ordered() ([]string, map[string]extEntry, []error) {
	names, err := g.Names()
	if err != nil {
		return nil, nil, []error{err}
	}
	entries := map[string]extEntry{}
	var errs []error
	var ok []string
	for _, name := range names {
		e, err := g.Read(name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		entries[name] = e
		ok = append(ok, name)
	}
	sort.SliceStable(ok, func(i, j int) bool {
		a, b := entries[ok[i]].RegisteredAt, entries[ok[j]].RegisteredAt
		if !a.Equal(b) {
			return a.Before(b)
		}
		return ok[i] < ok[j]
	})
	return ok, entries, errs
}

// write stores an entry atomically (temp file and rename).
func (g extRegistry) write(name string, e extEntry) error {
	if err := os.MkdirAll(g.Dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(g.Dir, "."+name+"-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), g.path(name))
}

// remove deletes name's registry file.
func (g extRegistry) remove(name string) error {
	err := os.Remove(g.path(name))
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("extension %s is not registered", name)
	}
	return err
}

// resolveExtBin is the absolute path of bin: a path when it has a
// separator, else the first executable of that name on PATH.
func resolveExtBin(bin string) (string, error) {
	path, err := exec.LookPath(bin)
	if err != nil {
		return "", fmt.Errorf("bin %s: %w", bin, err)
	}
	return filepath.Abs(path)
}

// execExt runs an extension bin with args and env added to mtm's
// environment, and returns its stdout; a failure carries the command and
// its stderr.
func execExt(ctx context.Context, bin string, env []string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, extExecTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	// A killed tool's children may hold its output open: stop waiting
	// for them shortly after the kill.
	cmd.WaitDelay = extWaitDelay
	cmd.Env = extensionExecEnv(env, len(args) > 0 && args[0] == "catalog")
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		words := filepath.Base(bin) + " " + strings.Join(args[:min(2, len(args))], " ")
		if msg := strings.TrimSpace(errOut.String()); msg != "" {
			return nil, fmt.Errorf("%s failed: %w: %s", words, err, msg)
		}
		return nil, fmt.Errorf("%s failed: %w", words, err)
	}
	return out.Bytes(), nil
}

// parseExtCatalog decodes and checks a catalog answer.
func parseExtCatalog(bin string, data []byte) (ocli.CatalogDoc, error) {
	var doc ocli.CatalogDoc
	var head map[string]json.RawMessage
	if err := json.Unmarshal(data, &head); err != nil {
		return doc, fmt.Errorf("%s catalog: not a JSON object: %w", bin, err)
	}
	for _, field := range []string{"schema", "name", "nouns"} {
		if _, ok := head[field]; !ok {
			return doc, fmt.Errorf("%s catalog: missing required field %q", bin, field)
		}
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return doc, fmt.Errorf("%s catalog: %w", bin, err)
	}
	if doc.Schema != ocli.Schema {
		return doc, fmt.Errorf("%s catalog: schema %d, mtm reads schema %d", bin, doc.Schema, ocli.Schema)
	}
	for i, n := range doc.Nouns {
		if n.Name == "" {
			return doc, fmt.Errorf("%s catalog: nouns[%d] has no name", bin, i)
		}
		for k, v := range n.Verbs {
			if v.Name == "" {
				return doc, fmt.Errorf("%s catalog: noun %s verbs[%d] has no name", bin, n.Name, k)
			}
		}
	}
	return doc, nil
}

// extPlan keeps a catalog's nouns with at least one mountable verb
// (extMountable) and no modal pin, with only those verbs: the framework's
// own verbs run inside the tool's own screen, so a host has nothing to
// mount for them. The tool's own verbs are the catalog's AppNouns (every
// verb but the ones marked builtin); a catalog from a tool built before
// ochazuke v0.8.0 marks none, so for it the framework's are the keys a
// fresh runtime registers (frameworkVerbs).
func extPlan(doc ocli.CatalogDoc) []ocli.CatalogNoun {
	nouns, fallback := doc.AppNouns(), !catalogMarksBuiltin(doc)
	var out []ocli.CatalogNoun
	for _, n := range nouns {
		var verbs []ocli.CatalogVerb
		for _, v := range n.Verbs {
			if extMountable(n.Name, v, fallback) {
				verbs = append(verbs, v)
			}
		}
		if len(verbs) == 0 || (n.Pin != "" && n.Pin != "normal") {
			continue
		}
		n.Verbs = verbs
		out = append(out, n)
	}
	return out
}

// catalogMarksBuiltin reports a catalog that carries the builtin field:
// every ochazuke tool's catalog lists the framework's verbs, so one built
// on v0.8.0 or later has at least one verb marked builtin.
func catalogMarksBuiltin(doc ocli.CatalogDoc) bool {
	for _, n := range doc.Nouns {
		for _, v := range n.Verbs {
			if v.Builtin {
				return true
			}
		}
	}
	return false
}

// extMountable reports a catalog verb mtm mounts: an application verb the
// tool declared. Kind alone is not enough: the framework's tree verbs
// (outline.*) have application handlers. A catalog that marks builtin
// verbs has already dropped them (AppNouns); for an older one (fallback)
// a verb under a framework key (frameworkVerbs) is the framework's.
func extMountable(noun string, v ocli.CatalogVerb, fallback bool) bool {
	return v.Kind == ochazuke.KindApplication && !(fallback && frameworkVerbs()[noun+"."+v.Name])
}

// frameworkVerbs is every operation key a fresh runtime and model register
// (ochazuke's own registry), so every ochazuke tool's catalog lists them.
// An application cannot override one with an application handler
// (NounBuilder.Verb panics), so an application verb under such a key is
// the framework's; a tool's own verb on a framework noun
// (history.capture-hook) is not under one and still mounts.
var frameworkVerbs = sync.OnceValue(func() map[string]bool {
	r := ochazuke.NewRuntime()
	ochazuke.NewModel(r)
	keys := make(map[string]bool, 2*r.Operations.Len())
	for key := range r.Operations.Entries() {
		keys[key] = true
		// An older catalog names the framework's nouns by their short
		// names (outline.expand, before ochazuke's namespace).
		keys[r.DisplayKey(key)] = true
	}
	return keys
})

// extHistoryKind is the history kind a mounted verb records: the catalog
// verb's own (CatalogVerb.History), so a tool's fetches stay out of the
// user's history in mtm as they do in the tool. None keeps the framework
// default (change, for an application verb); a kind the framework does not
// know is reported and the default.
func extHistoryKind(v ocli.CatalogVerb) (string, error) {
	switch v.History {
	case "", ochazuke.HistoryAction, ochazuke.HistoryChange, ochazuke.HistoryFetch:
		return v.History, nil
	}
	return "", fmt.Errorf("declares history %q, not action, change or fetch; recording it as a change", v.History)
}

// extension is one mounted extension.
type extension struct {
	name  string
	ns    string // the namespace its nouns mount under (extNamespace)
	bin   string
	doc   ocli.CatalogDoc
	nouns map[string]string // catalog noun -> mtm noun (<ext>.<noun>, extension_names.go)
	// naming is how its nouns were named when it mounted, and aliases
	// the old spellings mtm gave them (extension_names.go).
	naming       extNounNaming
	aliases      []string
	views        []*ocli.PageView
	entry        []byte                // the registry entry it was mounted from, as JSON (extension_sync.go)
	sources      map[string]*extSource // a parameter source (the tool's NOUN.VERB) -> its cached choices (extension_sources.go)
	bindingLists map[string]string     // host noun -> mounted list, shared catalog key policy
	bindingCRUD  map[string][]string
	// viaNouns is, per mounted noun, the mounted nouns whose rows its verbs
	// are named on: a catalog key row on a review row running the question
	// verb decide "via review.decide" says a review row is a thing that verb
	// acts on, so its id is the target (extTarget).
	viaNouns map[string]map[string]bool
	// fetchEnv is the environment its page loads run with, read on the
	// loop when its rows reload (extension_context.go).
	fetchEnv atomic.Pointer[[]string]
	// lists are its mounted lists' rows as filed, scoped by project
	// (extension_scope.go).
	lists map[string]*extListState
}

// extPageID is the page of an extension (a synthesized page) or of one of
// its declared pages.
func extPageID(ext, page string) string {
	if page == "" {
		return "ext/" + ext
	}
	return "ext/" + ext + "/" + page
}

// extEnv is what an extension's exec is told: mtm's data directory and its
// own name, for store.OpenExtension, and the project context
// (MTM_PROJECT_DIRS, extension_context.go). It reads mtm's state: call it
// on the loop.
func (a *App) extEnv(name string) []string {
	return append(a.baseExtEnv(name), a.contextEnv()...)
}

// baseExtEnv snapshots the active request's actor and host policy on the loop.
func (a *App) baseExtEnv(name string) []string {
	env := []string{"MTM_DATA=" + a.dataDir(), "MTM_EXTENSION=" + name, actorEnv + "=" + a.actorEnvValue(), "OCHAZUKE_EXTENSION=" + name}
	return append(env, a.hostEnv()...)
}

// dataDir is mtm's data directory: the directory of its database.
func (a *App) dataDir() string { return filepath.Dir(a.dataPath) }

// extRegistry is mtm's extension registry, under the data directory.
func (a *App) extRegistry() extRegistry {
	return extRegistry{Dir: filepath.Join(a.dataDir(), extensionsDir)}
}

// defineExtensions adds the extension noun.
func (a *App) defineExtensions() {
	a.r.Define("extension").
		Verb("register", a.registerExtension, "Register a family tool's catalog under NAME (its bin: --bin, else o-NAME or NAME on PATH); its pages are tabs after mtm's").Positional("name").
		Verb("all", a.listExtensions, "List the registered extensions").Fetch().
		Verb("peer-rows", a.peerRows, "List declared peer keys, whether loaded, and why a row was dropped").Fetch().
		Verb("peers", a.extensionPeers, "List registered peers' namespaces, versions and application verbs").Fetch().Positional("name").
		Verb("available", a.availableExtensions, "List the family tools on PATH (o-NAME answering catalog) and the registered extensions, each available, registered, stale or missing").Fetch().Quiet().
		Verb("refresh", a.refreshExtension, "Read a registered extension's catalog again (every extension when NAME is omitted)").Positional("name").
		Verb("remove", a.removeExtension, "Unregister an extension; --purge also deletes its data file").Positional("name").Confirm("Remove extension {{label}}? Its data file stays (mtm extension remove NAME --purge deletes it).")
	a.parameters("extension.peers", map[string]ochazuke.Parameter{"name": {Type: "string", Description: "Only this registered extension"}})
	a.parameters("extension.register", map[string]ochazuke.Parameter{
		"name": {Type: "string", Description: "The extension's name: its tab and the prefix of its nouns (lowercase letters, digits, dashes); on an Extensions row, the row's"},
		"bin":  {Type: "string", Description: "The tool's executable, a path or a name on PATH (default o-NAME, else NAME)"},
	})
	a.parameters("extension.refresh", map[string]ochazuke.Parameter{"name": {Type: "string", Description: "The extension; omitted, every one"}})
	a.parameters("extension.remove", map[string]ochazuke.Parameter{
		"name":  {Type: "string", Description: "The extension; on an Extensions row, the row's"},
		"purge": {Type: "boolean", Description: "Also delete the extension's data file (" + store.ExtensionDir + "/NAME.db under mtm's data directory)"},
	})
}

// extSummary is what register and refresh answer.
type extSummary struct {
	Name    string `json:"name"`
	Bin     string `json:"bin"`
	Version string `json:"version,omitempty"`
	File    string `json:"file"`
	Nouns   int    `json:"nouns"`
	Pages   int    `json:"pages"`
}

// registerExtension is extension.register: the catalog is read off the
// loop, the registry file written, and in the TUI the extension's pages
// mounted (re-mounted when it was registered before) at once.
func (a *App) registerExtension(ctx ochazuke.Ctx) ochazuke.Result {
	name := rowExtName(ctx)
	if !store.ValidExtensionName(name) {
		return ochazuke.Failed(fmt.Errorf("extension name %q: use lowercase letters, digits and dashes", name))
	}
	bin := ochazuke.StringArg(ctx.Args, "bin", "")
	if row, ok := extRow(ctx.Instance); ok && bin == "" && row.Name == name {
		bin = row.Bin // an available row registers the bin discovery found
	}
	if bin == "" {
		bin = "o-" + name
		if _, err := exec.LookPath(bin); err != nil {
			bin = name
		}
	}
	path, err := resolveExtBin(bin)
	if err != nil {
		return ochazuke.Failed(err)
	}
	reg := a.extRegistry()
	env := a.extEnv(name)
	file := reg.path(name)
	a.extFetchStart(name)
	return ochazuke.Pending(func(c context.Context) (any, error) {
		stamp := binStamp(path) // before the exec: a rebuild during it reads again
		out, err := execExt(c, path, env, "catalog")
		if err != nil {
			return nil, err
		}
		doc, err := parseExtCatalog(filepath.Base(path), out)
		if err != nil {
			return nil, err
		}
		return extRegistered{summaryOf(name, path, file, doc), doc, stamp}, nil
	}).Label(name).Then(func(ctx ochazuke.Ctx, v any, err error) {
		a.extFetchDone(name, err)
		if err != nil {
			return
		}
		doc := v.(extRegistered).doc
		e := extEntry{Bin: path, RegisteredAt: time.Now().UTC().Truncate(time.Second), Catalog: doc, BinStamp: v.(extRegistered).stamp}
		if old, rerr := reg.Read(name); rerr == nil {
			e.RegisteredAt = old.RegisteredAt // re-registering keeps its tab's place
		}
		if werr := reg.write(name, e); werr != nil {
			ctx.Error(werr.Error())
			return
		}
		a.extSyncDue = !a.cli
		a.extPage.due = true
	})
}

// extRegistered is register's answer: the summary, and the catalog the
// completion writes.
type extRegistered struct {
	extSummary
	doc   ocli.CatalogDoc
	stamp *extBinStamp
}

// summaryOf is what register and refresh answer for one extension.
func summaryOf(name, bin, file string, doc ocli.CatalogDoc) extSummary {
	return extSummary{Name: name, Bin: bin, Version: doc.Version, File: file, Nouns: len(extPlan(doc)), Pages: len(doc.Pages)}
}

// extListing is one row of extension.all.
type extListing struct {
	Name         string    `json:"name"`
	Bin          string    `json:"bin,omitempty"`
	Version      string    `json:"version,omitempty"`
	RegisteredAt time.Time `json:"registeredAt,omitzero"`
	Nouns        int       `json:"nouns"`
	Pages        int       `json:"pages"`
	Data         string    `json:"data,omitempty"` // the extension's data file, when it has one
	Error        string    `json:"error,omitempty"`
}

func (a *App) listExtensions(ochazuke.Ctx) ochazuke.Result {
	reg := a.extRegistry()
	names, err := reg.Names()
	if err != nil {
		return ochazuke.Failed(err)
	}
	out := []extListing{}
	for _, name := range names {
		e, err := reg.Read(name)
		if err != nil {
			out = append(out, extListing{Name: name, Error: err.Error()})
			continue
		}
		l := extListing{Name: name, Bin: e.Bin, Version: e.Catalog.Version, RegisteredAt: e.RegisteredAt, Nouns: len(extPlan(e.Catalog)), Pages: len(e.Catalog.Pages)}
		if path, err := store.ExtensionPath(a.dataDir(), name); err == nil {
			if _, err := os.Stat(path); err == nil {
				l.Data = path
			}
		}
		out = append(out, l)
	}
	return ochazuke.Done(out)
}

// refreshExtension is extension.refresh: each named extension's catalog is
// read again from the bin its file names and rewritten; in the TUI a
// changed catalog is re-mounted at once and the pages reload their rows
// (a running mtm sees a refresh from the CLI when a page is next opened).
func (a *App) refreshExtension(ctx ochazuke.Ctx) ochazuke.Result {
	reg := a.extRegistry()
	names := []string{rowExtName(ctx)}
	if names[0] == "" {
		var err error
		if names, err = reg.Names(); err != nil {
			return ochazuke.Failed(err)
		}
	}
	entries := map[string]extEntry{}
	for _, name := range names {
		e, err := reg.Read(name)
		if err != nil {
			return ochazuke.Failed(err)
		}
		entries[name] = e
	}
	envs := map[string][]string{}
	for _, name := range names {
		envs[name] = a.extEnv(name)
	}
	for _, name := range names {
		a.extFetchStart(name)
	}
	return ochazuke.Pending(func(c context.Context) (any, error) {
		out := []extRegistered{}
		var errs []error
		for _, name := range names {
			bin := entries[name].Bin
			stamp := binStamp(bin)
			answer, err := execExt(c, bin, envs[name], "catalog")
			var doc ocli.CatalogDoc
			if err == nil {
				doc, err = parseExtCatalog(filepath.Base(bin), answer)
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", name, err))
				continue
			}
			out = append(out, extRegistered{summaryOf(name, bin, reg.path(name), doc), doc, stamp})
		}
		// The catalogs read are written even when another failed.
		return out, errors.Join(errs...)
	}).Then(func(ctx ochazuke.Ctx, v any, err error) {
		done, _ := v.([]extRegistered)
		for _, name := range names {
			if slices.ContainsFunc(done, func(r extRegistered) bool { return r.Name == name }) {
				a.extFetchDone(name, nil)
			} else {
				a.extFetchDone(name, cmp.Or(err, errors.New("no catalog")))
			}
		}
		for _, r := range done {
			name := r.Name
			e := entries[name]
			if err := a.storeExtensionCatalog(reg, name, e, r.doc, r.stamp); err != nil {
				ctx.Error(err.Error())
			}
		}
		a.extSyncDue = !a.cli
		a.extPage.due = true
	})
}

// storeExtensionCatalog is the shared completion of explicit and automatic
// refresh. Only a valid catalog reaches here; a failed write keeps the cache.
func (a *App) storeExtensionCatalog(reg extRegistry, name string, e extEntry, doc ocli.CatalogDoc, stamp *extBinStamp) error {
	e.Catalog, e.BinStamp = doc, stamp
	if err := reg.write(name, e); err != nil {
		return err
	}
	delete(a.extPage.failed, name)
	if x := a.extension(name); x != nil && !a.cli {
		a.refreshExtensionRows(x)
	}
	a.extSyncDue = !a.cli
	a.extPage.due = true
	return nil
}

// removeExtension is extension.remove (confirmed): the registry file goes,
// and with --purge the extension's data file; its pages close.
func (a *App) removeExtension(ctx ochazuke.Ctx) ochazuke.Result {
	name := rowExtName(ctx)
	if name == "" {
		return ochazuke.Failed(fmt.Errorf("extension remove: give NAME"))
	}
	if err := a.extRegistry().remove(name); err != nil {
		return ochazuke.Failed(err)
	}
	a.extPage.due = true
	result := map[string]any{"name": name, "removed": true}
	if purge, _ := ctx.Args["purge"].(bool); purge {
		path, err := store.ExtensionPath(a.dataDir(), name)
		if err != nil {
			return ochazuke.Failed(err)
		}
		for _, p := range []string{path, path + "-wal", path + "-shm"} {
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				return ochazuke.Failed(err)
			}
		}
		result["purged"] = path
	}
	if x := a.extension(name); x != nil {
		err := a.unmountExtension(x)
		for i, y := range a.exts {
			if y == x {
				a.exts = append(a.exts[:i], a.exts[i+1:]...)
				break
			}
		}
		a.refreshSides()
		a.reloadPeerBindings()
		if err != nil {
			return ochazuke.Failed(err)
		}
	}
	return ochazuke.Done(result)
}

// extension is the mounted extension of this name, or nil.
func (a *App) extension(name string) *extension {
	for _, x := range a.exts {
		if x.name == name {
			return x
		}
	}
	return nil
}

// mountExtensions mounts every registered extension in tab order; a file
// that cannot be read or mounted is a notice, never a failed start.
func (a *App) mountExtensions() {
	names, entries, errs := a.orderedForMount()
	if a.cli {
		a.startRegistry = &registrySnapshot{names, entries}
	}
	a.notifyExtErrs(errs)
	a.mountBatch(func() {
		for _, name := range names {
			a.mountExtension(name, entries[name])
		}
	})
	a.syncBareNounNames()
	a.reloadPeerBindings()
}

// mountBatch runs mounts that share one reload of the sides, the user
// actions and the user bindings. Each mount asks for that reload; inside a
// batch the asks collapse to one run after the last mount, since only the
// result of the last reload is ever seen (a reload replaces the previous
// one's rows).
func (a *App) mountBatch(mounts func()) {
	if a.mountDepth > 0 {
		mounts()
		return
	}
	a.mountDepth++
	a.mountReload = false
	mounts()
	a.mountDepth--
	if a.mountReload {
		a.mountReload = false
		a.reloadMounted()
	}
}

// reloadMounted reapplies what a mount changes: the sides, then the user's
// actions before bindings, so their rows can name the mounted actions.
func (a *App) reloadMounted() {
	a.mountReloads++
	a.refreshSides()
	a.r.ReloadUserActions()
	a.r.ReloadUserBindings()
}

// mountExtension adds one extension: a proxy verb on mtm's noun for every
// application verb, its catalog key rows that do not shadow a row on their
// path, and its pages (declared data pages, else one synthesized page).
// It is listed in a.exts even when nothing could be mounted, so a sync
// does not try again until its registry entry changes.
func (a *App) mountExtension(name string, e extEntry) {
	notify := func(format string, args ...any) { a.r.UI().Notify(name + ": " + fmt.Sprintf(format, args...)) }
	ns, nsErr := extNamespace(name, e.Bin, e.Catalog)
	x := &extension{name: name, ns: ns, bin: e.Bin, doc: e.Catalog, nouns: map[string]string{}, entry: entryJSON(e)}
	defer func() {
		a.exts = append(a.exts, x)
		// Mounting replaces the noun's verbs and key rows; the user's
		// actions and bindings are reapplied (mountBatch shares it).
		if a.mountDepth > 0 {
			a.mountReload = true
			return
		}
		a.reloadMounted()
	}()
	naming, err := a.extNamingFor(ns)
	if nsErr != nil {
		notify("%v", nsErr)
	}
	if err != nil {
		notify("%v", err)
	}
	x.naming = naming
	plan := extPlan(e.Catalog)
	doc := e.Catalog
	doc.Nouns = plan
	for i := range doc.Nouns {
		for j := range doc.Nouns[i].Verbs {
			v := &doc.Nouns[i].Verbs[j]
			kind, err := extHistoryKind(*v)
			if err != nil {
				notify("%s %s %v", doc.Nouns[i].Name, v.Name, err)
			}
			v.History = kind
		}
	}
	canonicalKeys := map[string]string{}
	mounted := ocli.MountCatalog(a.r, doc, ocli.HostMount{
		Local: func(noun string) string { return naming.local(ns, noun) },
		Param: extParameter,
		Proxy: func(noun string, v ocli.CatalogVerb) any { return a.extProxy(x, noun, v.Name, v.OpensPage) },
		CanonicalKey: func(noun, selector, key string) error {
			cache := noun + "\x00" + selector
			if selector == key || canonicalKeys[cache] == key {
				return nil
			}
			canonicalKeys[cache] = key
			if err := a.rekeyExtensionReferences(x, noun, selector, key); err != nil {
				delete(canonicalKeys, cache)
				return err
			}
			return nil
		},
	})
	x.nouns = mounted.Nouns
	for _, warning := range mounted.Warnings {
		notify("%s", warning)
	}
	if len(x.nouns) == 0 {
		if len(e.Catalog.Sides) == 0 {
			notify("no application nouns to mount")
		}
		return
	}
	a.mountOldNounNames(x, notify)
	a.mountSources(x, plan)
	a.syncFetchEnv(x)
	a.declareExtScope(name)
	pages := pagesOf(name, e.Catalog, plan, x.nouns)
	cfg := ocli.PageConfig{
		Noun: func(n string) string {
			if local, ok := x.nouns[n]; ok {
				return local
			}
			return naming.local(ns, n)
		},
		Fetch: func(c context.Context, noun, verb string) ([]byte, error) {
			return execExt(c, x.bin, x.loadEnv(nil), noun, verb, "--json", "{}")
		},
		FetchRecord: func(c context.Context, noun, key string) ([]byte, error) {
			field := extensionKeyProp(x.doc, noun)
			if field == "" {
				return nil, fmt.Errorf("%s has no key prop", noun)
			}
			args, err := json.Marshal(ochazuke.Arguments{field: key})
			if err != nil {
				return nil, err
			}
			return execExt(c, x.bin, x.loadEnv(nil), noun, "get", "--json", string(args))
		},
		// The state verb asks for what changed since the rows the page
		// holds (ochazuke's state deltas), when the catalog declares
		// stateDelta; an older tool keeps full answers through Fetch.
		FetchFlags: func(c context.Context, noun, verb string, flags []string) ([]byte, error) {
			return execExt(c, x.bin, x.loadEnv(nil), append([]string{noun, verb, "--json", "{}"}, flags...)...)
		},
		// Rows that land are scoped by project (extension_scope.go).
		OnRows: func(list string, rows []ocli.Row) { a.extRowsLanded(x, list, rows) },
		// Every page's detail shows the notes about the selected row (the
		// same stack as mtm's own details: notes.related-hidden keeps to
		// them, notes.related-empty hides it while there are none).
		Related: &ocli.RelatedConfig{Hidden: settingRelatedHidden, Empty: settingRelatedEmpty},
	}
	a.mountExtensionSegments(x, cfg.Noun)
	for _, p := range pages {
		v, err := ocli.MountPage(a.m, e.Catalog, p, cfg)
		if err != nil {
			notify("page %s: %v", p.ID, err)
			continue
		}
		x.views = append(x.views, v)
		// An extension's page reloads its rows when it is opened (off the
		// loop; the rows on screen stay until they land). A rebuilt bin's
		// catalog is read again first; the refresh reloads the rows once it
		// lands.
		a.r.PageOf(v.Page()).OnOpen = func(*ochazuke.Runtime, string) {
			a.checkStaleExtensions(x.name)
			a.reloadExtension(x)
		}
		x.trackExtLists(v)
		for _, l := range p.Lists {
			if local, ok := x.nouns[l.Noun]; ok {
				if _, taken := a.owners[local]; !taken {
					a.owners[local] = l.ID // the CLI's verbs of the noun act for its list
				}
			}
		}
	}
	crudKeys := a.mountCRUD(x, plan, pages)
	if dropped := a.loadExtBindings(x, e.Catalog.Bindings, pages, crudKeys); len(dropped) > 0 {
		notify("key rows dropped: %s", strings.Join(dropped, ", "))
	}
}

// extParameter adapts a proxied parameter: a source names the tool's own
// verb, which mtm cannot run on the loop, so such a parameter takes any
// value (the tool still checks it); in the TUI its choices are a cache
// loaded off the loop (extension_sources.go).
func extParameter(p ochazuke.Parameter) ochazuke.Parameter {
	if p.Source != "" {
		p.Source, p.Free = "", true
	}
	return p
}

// pagesOf is what an extension shows: its declared data pages, each id
// prefixed with ext/<name>/ (lists too), else one page named after it with
// a list per mounted noun that has a row source and a list of verbs for a
// noun no other noun lists rows of; groups when there is more than one
// list. Every page shows the selected row's record.
func pagesOf(name string, doc ocli.CatalogDoc, plan []ocli.CatalogNoun, mounted map[string]string) []ocli.CatalogPage {
	var out []ocli.CatalogPage
	for _, p := range doc.Pages {
		if !p.IsData() {
			continue
		}
		p.ID = extPageID(name, p.ID)
		lists := make([]ocli.CatalogList, len(p.Lists))
		for i, l := range p.Lists {
			l.ID = "ext/" + name + "/" + l.ID
			lists[i] = l
		}
		p.Lists = lists
		out = append(out, p)
	}
	if len(out) > 0 {
		return out
	}
	listed := map[string]bool{} // nouns whose rows another noun's source lists
	for _, n := range plan {
		if verb, rows := doc.RowSource(n.Name); verb != "" && rows != n.Name {
			listed[rows] = true
		}
	}
	page := ocli.CatalogPage{ID: extPageID(name, ""), Title: name, Detail: &ocli.CatalogDetail{}}
	for _, n := range plan {
		if _, ok := mounted[n.Name]; !ok {
			continue
		}
		if verb, _ := doc.RowSource(n.Name); verb == "" && listed[n.Name] {
			continue // its rows live in another noun's list
		}
		page.Lists = append(page.Lists, ocli.CatalogList{ID: page.ID + "/" + n.Name, Noun: n.Name})
	}
	if len(page.Lists) == 0 {
		return nil
	}
	if len(page.Lists) > 1 {
		page.Layout = ocli.LayoutGroups
	}
	return []ocli.CatalogPage{page}
}

// loadExtBindings installs the catalog's key rows of mounted nouns, renamed
// to mtm's nouns. A row naming one of the tool's own instances, running a
// verb mtm did not mount, whose keys clash with a row of a noun on its
// list's path (the list, the page, app: keyTaken), or whose sequence clashes
// with mtm's CRUD keys on that noun (n, i, d d) is dropped and named
// in the answer. A sequence's generated prefix cancel ("g esc" for "g s")
// is skipped silently: loading the sequence brings its own back.
func (a *App) loadExtBindings(x *extension, rows []ochazuke.Binding, pages []ocli.CatalogPage, crud map[string][]string) []string {
	home := map[string]string{} // catalog noun -> a list its rows live in
	for _, p := range pages {
		for _, l := range p.Lists {
			if _, ok := home[l.Noun]; !ok {
				home[l.Noun] = l.ID
			}
			if _, rows := x.doc.RowSource(l.Noun); rows != "" {
				if _, ok := home[rows]; !ok {
					home[rows] = l.ID
				}
			}
		}
	}
	x.bindingLists = make(map[string]string, len(home))
	for noun, list := range home {
		x.bindingLists[x.nouns[noun]] = list
	}
	x.bindingCRUD = crud
	x.viaNouns = map[string]map[string]bool{}
	var dropped []string
	for _, row := range rows {
		local, ok := x.nouns[row.Noun]
		if !ok || isPrefixCancel(row) {
			continue
		}
		if slices.Contains(crud[local], "d d") && catalogDeleteRow(row, row.Noun) {
			continue // mountCRUD already installed this row with its argument sources.
		}
		where := row.Keys + row.Pattern + " on " + row.Noun
		req, mounted := x.nouns[row.Request.Noun]
		if row.Instance != "" || !mounted {
			dropped = append(dropped, where+": instance-only row or unmounted action noun")
			continue
		}
		if i := strings.LastIndex(row.Request.Via, "."); i > 0 && row.Request.Via[:i] == row.Noun && row.Noun != row.Request.Noun {
			if x.viaNouns[req] == nil {
				x.viaNouns[req] = map[string]bool{}
			}
			x.viaNouns[req][local] = true
		}
		row.Request.Noun = req
		row.Noun = local
		// An application guard runs only in the producing runtime, so it is
		// left off; a portable guard (a built-in, a row guard such as
		// row:!item) reads only the key and the row, and stays: mtm judges
		// it on the mounted row as the tool does. Host and user binding
		// files still validate their own guards normally.
		if !ochazuke.PortableGuard(row.When) {
			row.When = ""
		}
		if reason := a.extBindingPolicy(row, x.bindingLists, crud); reason != "" {
			dropped = append(dropped, where+": "+reason)
			continue
		}
		data, err := json.Marshal([]ochazuke.Binding{row})
		if err == nil {
			_, err = a.r.LoadBindings(data)
		}
		if err != nil {
			dropped = append(dropped, where+": "+err.Error())
		}
	}
	return dropped
}

func (x *extension) nounOfLocal(local string) string {
	for n, l := range x.nouns {
		if l == local {
			return n
		}
	}
	return ""
}

// isPrefixCancel reports whether a row is the cancel ochazuke generates
// for a key-sequence prefix: "<prefix> esc" running ochazuke.mode.leave.
func isPrefixCancel(b ochazuke.Binding) bool {
	return strings.HasSuffix(b.Keys, " "+ochazuke.CancelKey) && b.Request.Key() == "ochazuke.mode.leave" && len(b.From) == 0 && b.When == ""
}

// keyTaken reports whether a noun on a list's path (the list, its page,
// app) binds keys that clash with the row's, in the row's mode or in every
// mode: the same keys, or one sequence a prefix of the other (an extension
// "g" would make mtm's "g g" wait, and its "g g x" would shadow "g g").
// Sequences that only share a prefix ("g s" beside "g g") do not clash.
// Pattern rows and generated prefix cancels are not compared.
func (a *App) keyTaken(list string, row ochazuke.Binding) bool {
	if row.Keys == "" {
		return false
	}
	keys := strings.Fields(row.Keys)
	for _, id := range a.r.Ancestry(list) {
		n := a.r.Noun(a.r.Instances[id].Noun)
		if n == nil {
			continue
		}
		for mode, bindings := range n.Bindings {
			if mode != row.Mode && mode != "" && row.Mode != "" {
				continue
			}
			for _, b := range bindings {
				if b.Keys != "" && !isPrefixCancel(b) && keysClash(keys, strings.Fields(b.Keys)) {
					return true
				}
			}
		}
	}
	return false
}

// extProxy is the handler of one mounted verb: `<bin> NOUN VERB [--target
// ID] --json ARGS --yes` off the loop (mtm asked the verb's confirm
// already); the target is the nearest row of the verb's noun at the
// focus (extTarget). Catalog metadata selects capture or terminal handoff;
// UI dispatch presents text results, and the extension's rows reload.
func (a *App) extProxy(x *extension, noun, verb, opensPage string) ochazuke.AppHandler {
	return func(c ochazuke.Ctx) ochazuke.Result {
		// A verb the catalog says opens a page of the tool opens that page
		// here, when this host mounted it: the tool would only start its
		// own terminal program, with no terminal behind a job.
		if opensPage != "" && !a.cli {
			if id := extPageID(x.name, opensPage); a.r.PageOf(id) != nil {
				_, view := a.extViewOf(id)
				key := extensionOpenSubject(x.doc, noun, verb, c.Args, a.extTarget(x, noun, c.Instance))
				if view != nil && key != "" {
					if err := view.OpenRow(noun, key); err != nil {
						return ochazuke.Failed(err)
					}
					return ochazuke.Done(nil)
				}
				if err := a.r.OpenPage(id); err != nil {
					return ochazuke.Failed(err)
				}
				return ochazuke.Done(nil)
			}
		}
		args := c.Args
		op, _ := a.r.Operation(x.nouns[noun] + "." + verb)
		target := a.rowTargetUnlessSubjectGiven(op, args, a.extTarget(x, noun, c.Instance))
		if _, declared := op.Parameters["target"]; !declared {
			if explicit, ok := args["target"].(string); ok {
				target = explicit
				args = maps.Clone(args)
				delete(args, "target")
			}
		}
		if !a.cli {
			if name, p, ok := missingNounParam(x.doc, noun, verb, args); ok {
				return a.askNounParam(x, noun, verb, target, name, p, args)
			}
		}
		return a.extRun(x, noun, verb, target, args, nil)
	}
}

// rowTargetUnlessSubjectGiven is target, or "" when the verb takes its
// subject from the row (a parameter whose From is row or row:FIELD) and the
// request carries that value: the tool then has the subject by name, and
// the row's list id (culprit/NAME) is a handle only mtm's list knows; a
// tool resolves its own --target only when it reveals the row, so sending
// both makes it refuse an id it never needed.
func (a *App) rowTargetUnlessSubjectGiven(op ochazuke.Operation, args map[string]any, target string) string {
	if op.Subject == ochazuke.SubjectCollection {
		return ""
	}
	if target == "" {
		return target
	}
	for name, p := range op.Parameters {
		if p.From != ochazuke.SourceRow && !strings.HasPrefix(p.From, ochazuke.SourceRow+":") {
			continue
		}
		if v, ok := args[name]; ok && v != nil && v != "" {
			return ""
		}
	}
	return target
}

// extTarget is the tool's id of the nearest thing of noun (a catalog
// noun) from inst up its parents: the row a key ran on when it is of the
// verb's noun, else an ancestor that is, else none. A row of another noun
// is never the target: a key row on a clip running session.toggle runs
// with no target, as ochazuke's "the nearest thing of its noun" says.
func (a *App) extTarget(x *extension, noun string, inst *ochazuke.Instance) string {
	if inst == nil {
		return ""
	}
	want := a.r.CanonicalNoun(x.naming.local(x.ns, noun))
	if local, ok := x.nouns[noun]; ok {
		want = a.r.CanonicalNoun(local)
	}
	named := x.viaNouns[x.nouns[noun]]
	for _, id := range a.r.Ancestry(inst.ID) {
		in := a.r.Instances[id]
		if in == nil {
			continue
		}
		if cn := a.r.CanonicalNoun(in.Noun); cn != want && !named[in.Noun] && !named[cn] {
			continue
		}
		if target, ok := in.Data.(string); ok {
			return target
		}
	}
	return ""
}

// extRun delegates a mounted verb to the shared catalog proxy. The runtime
// presents a captured answer by the kind its catalog row declares (text,
// Markdown and preformatted in their viewer, rows in the results table;
// keys.text-results, keys.table-results), and OnPresented then clears the
// status line below; any other answer stays that one line, the page's
// status. Foreground verbs get the terminal. The callback sees the answer
// before refreshing rows or presentation.
func (a *App) extRun(x *extension, noun, verb, target string, args map[string]any, then func(v any, err error)) ochazuke.Result {
	words := []string{noun, verb}
	if target != "" {
		words = append(words, "--target", target)
	}
	js := []byte("{}")
	if len(args) > 0 {
		var err error
		if js, err = json.Marshal(args); err != nil {
			return ochazuke.Failed(err)
		}
	}
	words = append(words, "--json", string(js), "--yes")
	bin, env := x.bin, a.extEnv(x.name)
	project := a.contextProject() // the owner a row it makes or acts on gets (extension_scope.go)
	op, _ := a.r.Operation(x.nouns[noun] + "." + verb)
	_, currentView := a.extViewOf(a.r.Page)
	spec := ocli.CatalogVerb{Name: noun + " " + verb, Foreground: op.Foreground, Result: op.Result}
	if op.Timeout > 0 {
		spec.Timeout = op.Timeout.String()
	}
	if a.quitting && !spec.Foreground {
		// mtm is closing: no job would run, so the last write runs here.
		out, err := execExt(context.Background(), bin, env, words...)
		if err != nil {
			return ochazuke.Failed(err)
		}
		return ochazuke.Done(extDecoded(out))
	}
	var before map[string]any
	var preflight func(context.Context) error
	key := extensionSubjectKey(x.doc, noun, args, target)
	if op.Standard == "" && op.HistoryKind == ochazuke.HistoryChange && !op.Creates && !spec.Foreground && key != "" && extensionHasHumanProp(x.doc, noun) {
		preflight = func(ctx context.Context) error {
			js, err := json.Marshal(ochazuke.Arguments{extensionKeyProp(x.doc, noun): key})
			if err != nil {
				return err
			}
			raw, err := execExt(ctx, bin, env, noun, "get", "--json", string(js))
			if err != nil {
				return err
			}
			return json.Unmarshal(raw, &before)
		}
	}
	result := ocli.RunMounted(spec, ocli.ProxyCommand{
		Bin: bin, Args: words, Env: extensionExecEnv(env, false),
		Timeout: extExecTimeout, WaitDelay: extWaitDelay, Before: preflight,
	})
	if preflight != nil {
		result = result.Continue(func(value any, err error) (func(context.Context) (any, error), any, error) {
			if err == nil {
				_, err = a.r.CanonicalThingKey(ochazuke.Ctx{Runtime: a.r}, x.nouns[noun], key, ochazuke.Thing(before))
			}
			return nil, value, err
		})
	}
	return result.Label(x.name).Then(func(_ ochazuke.Ctx, v any, err error) {
		if err == nil {
			a.recordExtOwners(x, noun, verb, target, project, v)
		}
		if then != nil {
			then(v, err)
		}
		line := extResultLine(noun+" "+verb, v, err)
		for _, view := range x.views {
			view.SetStatus(line)
		}
		if a.r.Settings().Bool(settingExtPeerStatus) && currentView != nil && !slices.Contains(x.views, currentView) {
			currentView.SetStatus(line)
		}
		if !a.cli {
			a.reloadExtension(x)
		}
	}).OnPresented(func(ochazuke.Ctx) {
		for _, view := range x.views {
			view.SetStatus("")
		}
	})
}

// reloadExtension reloads every page of an extension and its parameter
// choices (off the loop).
func (a *App) reloadExtension(x *extension) {
	a.syncFetchEnv(x)
	for _, v := range x.views {
		if err := v.Reload(); err != nil {
			v.SetStatus(err.Error())
		}
	}
	if err := a.loadSources(x); err != nil && len(x.views) > 0 {
		x.views[0].SetStatus(err.Error())
	}
}

// refreshExtensionRows is an explicit refresh (extension.refresh): every
// page forgets the hashes of the rows it holds and asks for every list in
// full (PageView.Refresh), then the parameter choices reload.
func (a *App) refreshExtensionRows(x *extension) {
	a.syncFetchEnv(x)
	for _, v := range x.views {
		if err := v.Refresh(); err != nil {
			v.SetStatus(err.Error())
		}
	}
	if err := a.loadSources(x); err != nil && len(x.views) > 0 {
		x.views[0].SetStatus(err.Error())
	}
}

// extViewOf is the mounted page view of a page id, or nil.
func (a *App) extViewOf(page string) (*extension, *ocli.PageView) {
	for _, x := range a.exts {
		for _, v := range x.views {
			if v.Page() == page {
				return x, v
			}
		}
	}
	return nil, nil
}

// extDecoded is stdout as JSON, else as trimmed text.
func extDecoded(out []byte) any {
	var v any
	if err := json.Unmarshal(out, &v); err != nil {
		return strings.TrimSpace(string(out))
	}
	return v
}

// extResultLine is the page's status after a verb.
func extResultLine(what string, v any, err error) string {
	if err != nil {
		return what + ": " + err.Error()
	}
	data, _ := json.Marshal(v)
	return what + ": " + string(data)
}

// revealExtTarget makes a stand-in row for a CLI --target of a mounted
// extension: a row id as the TUI shows it (<list>/<id>), whose Data is the
// tool's own id. It reports whether target named one.
func (a *App) revealExtTarget(target string) bool {
	for _, x := range a.exts {
		for _, v := range x.views {
			for _, l := range v.Spec().Lists {
				id, ok := strings.CutPrefix(target, l.ID+"/")
				if !ok || id == "" {
					continue
				}
				noun := x.naming.local(x.ns, l.Noun)
				if verb, rows := x.doc.RowSource(l.Noun); verb != "" {
					noun = x.naming.local(x.ns, rows)
				}
				inst := a.r.Instance(target, ochazuke.Parent(l.ID), ochazuke.Kind(noun))
				inst.Data, inst.Label = id, id
				return true
			}
		}
	}
	return false
}

// keysClash reports whether one key sequence is a prefix of the other
// (equal sequences included).
func keysClash(a, b []string) bool {
	n := min(len(a), len(b))
	return slices.Equal(a[:n], b[:n])
}
