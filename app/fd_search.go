package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/handlers"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/picker"
	"ofam.dev/ochazuke/widgets/toast"
)

// Commands for configurable fd/fdfind file search and its results picker.
const (
	CmdFDSearch         = "search.files"
	CmdFDSearchAgain    = "search.files.again"
	CmdFDResultYank     = "search.files.result.yank"
	CmdFDResultOpenWith = "search.files.result.openWith"
	CmdFDResultAttach   = "search.files.result.attach"
	CmdFDResultRefine   = "search.files.result.refine"

	fdSearchModalID     = "fd.search"
	fdResultsModalID    = "fd.results"
	defaultFDMaxResults = 200
	fdSearchTimeout     = 15 * time.Second
)

var (
	fdEntryTypes = []string{"any", "file", "dir", "symlink", "executable", "empty"}
	fdCaseModes  = []string{"smart", "sensitive", "insensitive"}
)

type fdResult struct {
	FullPath string
	Label    string
	IsDir    bool
}

func fdSearchSpecs() []core.CommandSpec {
	return []core.CommandSpec{
		{
			ID:          CmdFDSearch,
			Description: "Search files using fd/fdfind with configurable flags and filters",
			Params: []core.ParamSpec{
				{Name: "dir", Type: core.ParamPath, Description: "initial search directory"},
			},
		},
		spec(CmdFDSearchAgain, "Re-open the fd file search form pre-filled with the previous search"),
		{ID: CmdFDResultYank, Description: "Yank the focused search result's full path", Meta: map[string]any{"internal": true}},
		{ID: CmdFDResultOpenWith, Description: "Pick a handler to open the focused search result", Meta: map[string]any{"internal": true}},
		{ID: CmdFDResultAttach, Description: "Attach the focused search result to the focused record", Tags: []string{"editing"}, Meta: map[string]any{"internal": true}},
		{ID: CmdFDResultRefine, Description: "Refine the file search with previous form values", Meta: map[string]any{"internal": true}},
	}
}

func fdSearchBindings() []core.Binding {
	return []core.Binding{
		bind(ModeNormal, "", "g F", CmdFDSearch, "search files (fd)"),
		bind(ModePicker, fdResultsModalID, "y", CmdFDResultYank, "yank path"),
		bind(ModePicker, fdResultsModalID, "o", CmdFDResultOpenWith, "open with"),
		bind(ModePicker, fdResultsModalID, "a", CmdFDResultAttach, "attach to record"),
		bind(ModePicker, fdResultsModalID, "r", CmdFDResultRefine, "refine search"),
		hidden(bind(ModePicker, fdResultsModalID, "e", CmdFDResultRefine, "refine search")),
	}
}

// ResolveFDBinary locates the fd/fdfind executable using configured override
// (tools.fdBinary) or PATH lookup (fd, then fdfind).
func ResolveFDBinary(configured string, lookPath func(string) (string, error)) (string, error) {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	cfg := strings.TrimSpace(configured)
	if cfg != "" {
		expanded := expandHome(cfg)
		if strings.Contains(expanded, "/") || strings.ContainsRune(expanded, filepath.Separator) {
			info, err := os.Stat(expanded)
			if err != nil {
				return "", fmt.Errorf("fd binary %q (from %s): %w", cfg, SetToolsFDBinary, err)
			}
			if info.IsDir() {
				return "", fmt.Errorf("fd binary %q (from %s) is a directory", cfg, SetToolsFDBinary)
			}
			return expanded, nil
		}
		p, err := lookPath(expanded)
		if err != nil {
			return "", fmt.Errorf("fd binary %q (from %s) not found in PATH: %w", cfg, SetToolsFDBinary, err)
		}
		return p, nil
	}
	for _, name := range []string{"fd", "fdfind"} {
		if p, err := lookPath(name); err == nil && p != "" {
			return p, nil
		}
	}
	return "", fmt.Errorf("neither fd nor fdfind found in PATH; install fd or configure %s in settings", SetToolsFDBinary)
}

// FDSearchSpecs returns the reactive, repeatable ParamSpecs for the fd search form.
func FDSearchSpecs(defaultDir string, defaultMaxResults int) []core.ParamSpec {
	var defaultPaths any
	if d := strings.TrimSpace(defaultDir); d != "" {
		defaultPaths = []string{d}
	}
	if defaultMaxResults < 0 {
		defaultMaxResults = 0
	}
	return []core.ParamSpec{
		{Name: "pattern", Type: core.ParamString, Description: "search pattern (regex or glob)"},
		{Name: "paths", Type: core.ParamPath, Repeatable: true, AddLabel: "search path", Default: defaultPaths, Description: "directories to search"},
		{Name: "type", Type: core.ParamString, Enum: slices.Clone(fdEntryTypes), Default: "any", Description: "entry type filter"},
		{Name: "extension", Type: core.ParamString, Repeatable: true, AddLabel: "extension", VisibleWhen: `type == "any" || type == "file"`, Description: "file extensions (e.g. go, md)"},
		{Name: "exclude", Type: core.ParamString, Repeatable: true, AddLabel: "exclude glob", Description: "glob patterns to exclude"},
		{Name: "maxDepth", Type: core.ParamInt, Description: "maximum search depth (0 = unlimited)"},
		{Name: "minDepth", Type: core.ParamInt, Description: "minimum search depth (0 = none)"},
		{Name: "size", Type: core.ParamString, VisibleWhen: `type == "any" || type == "file"`, Description: "file size filter (e.g. +1m, -100k)"},
		{Name: "changedWithin", Type: core.ParamString, Description: "modified within duration or date (e.g. 1d, 2weeks, 2026-01-01)"},
		{Name: "caseMode", Type: core.ParamString, Enum: slices.Clone(fdCaseModes), Default: "smart", Description: "case sensitivity mode"},
		{Name: "glob", Type: core.ParamBool, Description: "match pattern as a glob instead of regex"},
		{Name: "fullPath", Type: core.ParamBool, Description: "match pattern against full path"},
		{Name: "hidden", Type: core.ParamBool, Description: "include hidden files and directories"},
		{Name: "noIgnore", Type: core.ParamBool, Description: "do not respect .gitignore and ignore files"},
		{Name: "follow", Type: core.ParamBool, Description: "follow symbolic links"},
		{Name: "maxResults", Type: core.ParamInt, Default: defaultMaxResults, Description: "maximum number of results (0 = unlimited)"},
	}
}

// BuildFDArgs converts submitted form values into fd/fdfind CLI arguments and
// returns the primary search directory (workDir) for resolving relative output.
func BuildFDArgs(values map[string]any) (argv []string, workDir string, err error) {
	entryType := strings.TrimSpace(fdString(values, "type"))
	if entryType != "" && !slices.Contains(fdEntryTypes, entryType) {
		return nil, "", fmt.Errorf("invalid type %q (want one of %s)", entryType, strings.Join(fdEntryTypes, ", "))
	}
	caseMode := strings.TrimSpace(fdString(values, "caseMode"))
	if caseMode != "" && !slices.Contains(fdCaseModes, caseMode) {
		return nil, "", fmt.Errorf("invalid caseMode %q (want one of %s)", caseMode, strings.Join(fdCaseModes, ", "))
	}
	maxDepth, err := fdInt(values, "maxDepth")
	if err != nil {
		return nil, "", fmt.Errorf("maxDepth: %w", err)
	}
	if maxDepth < 0 {
		return nil, "", fmt.Errorf("maxDepth must be >= 0 (got %d)", maxDepth)
	}
	minDepth, err := fdInt(values, "minDepth")
	if err != nil {
		return nil, "", fmt.Errorf("minDepth: %w", err)
	}
	if minDepth < 0 {
		return nil, "", fmt.Errorf("minDepth must be >= 0 (got %d)", minDepth)
	}
	if minDepth > 0 && maxDepth > 0 && minDepth > maxDepth {
		return nil, "", fmt.Errorf("minDepth (%d) cannot exceed maxDepth (%d)", minDepth, maxDepth)
	}
	maxResults, err := fdInt(values, "maxResults")
	if err != nil {
		return nil, "", fmt.Errorf("maxResults: %w", err)
	}
	if maxResults < 0 {
		return nil, "", fmt.Errorf("maxResults must be >= 0 (got %d)", maxResults)
	}

	argv = []string{"--color=never"}

	switch entryType {
	case "file":
		argv = append(argv, "-t", "f")
	case "dir":
		argv = append(argv, "-t", "d")
	case "symlink":
		argv = append(argv, "-t", "l")
	case "executable":
		argv = append(argv, "-t", "x")
	case "empty":
		argv = append(argv, "-t", "e")
	}

	if entryType == "" || entryType == "any" || entryType == "file" {
		for _, rawExt := range fdStrings(values, "extension") {
			ext := strings.TrimLeft(strings.TrimSpace(rawExt), ".")
			if ext != "" {
				argv = append(argv, "-e", ext)
			}
		}
		if size := strings.TrimSpace(fdString(values, "size")); size != "" {
			argv = append(argv, "-S", size)
		}
	}

	for _, rawEx := range fdStrings(values, "exclude") {
		if ex := strings.TrimSpace(rawEx); ex != "" {
			argv = append(argv, "-E", ex)
		}
	}

	if maxDepth > 0 {
		argv = append(argv, "--max-depth", strconv.Itoa(maxDepth))
	}
	if minDepth > 0 {
		argv = append(argv, "--min-depth", strconv.Itoa(minDepth))
	}
	if cw := strings.TrimSpace(fdString(values, "changedWithin")); cw != "" {
		argv = append(argv, "--changed-within", cw)
	}

	switch caseMode {
	case "sensitive":
		argv = append(argv, "-s")
	case "insensitive":
		argv = append(argv, "-i")
	}

	if fdBool(values, "glob") {
		argv = append(argv, "-g")
	}
	if fdBool(values, "fullPath") {
		argv = append(argv, "-p")
	}
	if fdBool(values, "hidden") {
		argv = append(argv, "-H")
	}
	if fdBool(values, "noIgnore") {
		argv = append(argv, "-I")
	}
	if fdBool(values, "follow") {
		argv = append(argv, "-L")
	}
	if maxResults > 0 {
		argv = append(argv, "--max-results", strconv.Itoa(maxResults))
	}

	for _, rawPath := range fdStrings(values, "paths") {
		p := strings.TrimSpace(rawPath)
		if p == "" {
			continue
		}
		p = expandHome(p)
		if workDir == "" {
			workDir = p
		}
		argv = append(argv, "--search-path", p)
	}

	if pattern := strings.TrimSpace(fdString(values, "pattern")); pattern != "" {
		argv = append(argv, "--", pattern)
	}

	return argv, workDir, nil
}

func (a *App) defaultSearchDir(s core.State, c core.Command) string {
	if dir := strings.TrimSpace(c.Arg("dir")); dir != "" {
		return expandHome(dir)
	}
	if ref, ok := a.subject(s, c); ok {
		if rec, err := a.Store.Get(ref); err == nil {
			if dir := recordSearchDir(*rec); dir != "" {
				return dir
			}
		}
	}
	if pane, ok := a.SelectedPane(); ok {
		if dir := strings.TrimSpace(pane.Text("cwd")); dir != "" {
			return expandHome(dir)
		}
	}
	if cwd, err := os.Getwd(); err == nil && cwd != "" {
		return cwd
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return home
	}
	return "."
}

func recordSearchDir(rec data.Record) string {
	for _, field := range []string{"defaultDir", "cwd"} {
		if v := strings.TrimSpace(rec.Text(field)); v != "" && !handlers.IsURL(v) {
			return expandHome(v)
		}
	}
	for _, field := range []string{"filePath", "path"} {
		if v := strings.TrimSpace(rec.Text(field)); v != "" && !handlers.IsURL(v) {
			p := expandHome(v)
			if info, err := os.Stat(p); err == nil && info.IsDir() {
				return p
			}
			return filepath.Dir(p)
		}
	}
	return ""
}

func (a *App) fdAttachTarget(s core.State, c core.Command) *core.Ref {
	if ref, ok := a.subject(s, c); ok {
		r := ref
		return &r
	}
	if _, projRef, onProj := a.selectedRailProject(); onProj && projRef != nil {
		r := *projRef
		return &r
	}
	if pane, ok := a.SelectedPane(); ok {
		r := pane.Ref()
		return &r
	}
	return nil
}

func (a *App) fdMaxResultsDefault() int {
	if a != nil && a.cfg != nil && a.cfg.Settings != nil {
		if n := a.cfg.Settings.Int(SetSearchFDMaxResults, defaultFDMaxResults); n >= 0 {
			return n
		}
	}
	return defaultFDMaxResults
}

func (a *App) configuredFDBinary() string {
	if a != nil && a.cfg != nil && a.cfg.Settings != nil {
		return a.cfg.Settings.String(SetToolsFDBinary, "")
	}
	return ""
}

func (a *App) fdSearch(s core.State, c core.Command) core.StepResult {
	return core.Next(a.openFDSearchModal(s, c, false))
}

func (a *App) fdSearchAgain(s core.State, c core.Command) core.StepResult {
	return core.Next(a.openFDSearchModal(s, c, true))
}

func (a *App) openFDSearchModal(s core.State, c core.Command, reuseLast bool) core.State {
	defaultDir := a.defaultSearchDir(s, c)
	specs := FDSearchSpecs(defaultDir, a.fdMaxResultsDefault())
	var initial map[string]any
	if reuseLast && len(a.nav.lastFDSearch) > 0 {
		initial = cloneFDValues(a.nav.lastFDSearch)
	}
	attachTarget := a.fdAttachTarget(s, c)

	fm := form.New("Search Files (fd)", specs, initial)
	fm.SubmitRow = true
	fm.SubmitLabel = "Search"
	if a.cfg != nil && a.cfg.Settings != nil {
		fm.ShowAllFields = !a.cfg.Settings.Bool(SetFormReactiveFields, true)
	}

	var mod *modal.FormModal
	mod = modal.NewFormModal(fm, modal.FormOptions{
		ID:            fdSearchModalID,
		Scope:         fdSearchModalID,
		Title:         "Search Files (fd)",
		WidthPct:      76,
		HeightPct:     82,
		DisableDrafts: true,
		OnPickPath:    a.formPickPath(func() *modal.FormModal { return mod }),
		OnSubmit: func(vals map[string]any) modal.Result {
			argv, workDir, err := BuildFDArgs(vals)
			if err != nil {
				a.pushToast(err.Error(), toast.Err)
				return modal.Result{Handled: true, Status: err.Error(), SetStatus: true}
			}
			a.nav.lastFDSearch = cloneFDValues(vals)
			bin, err := ResolveFDBinary(a.configuredFDBinary(), exec.LookPath)
			if err != nil {
				a.pushToast(err.Error(), toast.Err)
				return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit, Status: err.Error(), SetStatus: true}
			}
			a.afterModal(func(st core.State) core.State {
				return a.runFDSearch(st, bin, argv, workDir, attachTarget)
			})
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	})
	a.say("j/k move · i edit · l/h cycle · +/x add/remove · p pick path · ctrl+s search")
	return a.pushModal(s, mod)
}

func (a *App) runFDSearch(s core.State, bin string, argv []string, workDir string, attachTarget *core.Ref) core.State {
	if a.opts.Live {
		a.say("searching files...")
		a.background(func(ctx context.Context) func(core.State) core.State {
			results, err := executeFDBinary(ctx, bin, argv, workDir)
			return func(st core.State) core.State {
				return a.presentFDResults(st, results, err, attachTarget)
			}
		})
		return s
	}
	results, err := executeFDBinary(context.Background(), bin, argv, workDir)
	return a.presentFDResults(s, results, err, attachTarget)
}

func executeFDBinary(parent context.Context, bin string, argv []string, workDir string) ([]fdResult, error) {
	ctx, cancel := context.WithTimeout(parent, fdSearchTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, argv...)
	if workDir != "" {
		if info, err := os.Stat(workDir); err == nil && info.IsDir() {
			cmd.Dir = workDir
		}
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	results := parseFDOutput(stdout.String(), workDir)
	if runErr != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return results, fmt.Errorf("file search timed out after %s", fdSearchTimeout)
		}
		if msg := firstLine(stderr.String()); msg != "" {
			return results, fmt.Errorf("%s: %s", filepath.Base(bin), msg)
		}
		return results, runErr
	}
	return results, nil
}

func parseFDOutput(raw, workDir string) []fdResult {
	baseDir := workDir
	if baseDir == "" {
		baseDir, _ = os.Getwd()
	}
	lines := strings.Split(raw, "\n")
	out := make([]fdResult, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" {
			continue
		}
		trailingDir := strings.HasSuffix(line, "/") || strings.HasSuffix(line, string(filepath.Separator))
		full := expandHome(line)
		if !filepath.IsAbs(full) && baseDir != "" {
			full = filepath.Join(baseDir, full)
		}
		full = filepath.Clean(full)
		isDir := trailingDir
		if info, err := os.Stat(full); err == nil {
			isDir = info.IsDir()
		}
		label := full
		if workDir != "" {
			if rel, err := filepath.Rel(workDir, full); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
				label = rel
			}
		}
		if isDir && !strings.HasSuffix(label, "/") {
			label += "/"
		}
		out = append(out, fdResult{FullPath: full, Label: label, IsDir: isDir})
	}
	return out
}

func (a *App) fdResultOpenTarget(res fdResult, from *core.Ref) openTarget {
	rules := a.cfg.Handlers.Match(res.FullPath)
	if len(rules) == 0 {
		if res.IsDir {
			rules = []handlers.Rule{
				{Name: handlers.PathPicker, Match: handlers.MatchDir, Kind: handlers.KindBuiltin},
				{Name: handlers.NewPane, Match: handlers.MatchDir, Kind: handlers.KindBuiltin},
			}
		} else {
			rules = []handlers.Rule{
				{Name: handlers.SideVim, Match: "*", Kind: handlers.KindBuiltin},
				{Name: handlers.SystemOpen, Match: "*", Kind: handlers.KindBuiltin},
			}
		}
	}
	return openTarget{
		path:  res.FullPath,
		title: filepath.Base(res.FullPath),
		from:  from,
		rules: rules,
	}
}

func (a *App) presentFDResults(s core.State, results []fdResult, runErr error, attachTarget *core.Ref) core.State {
	if runErr != nil && len(results) == 0 {
		a.pushToast(runErr.Error(), toast.Err)
		a.fail("search files", runErr)
		return s
	}
	if len(results) == 0 {
		a.pushToast("no files matched", toast.Info)
		a.say("no files matched")
		return s
	}

	items := make([]picker.Item, len(results))
	for i, res := range results {
		var badges []ui.Badge
		if res.IsDir {
			badges = append(badges, ui.Badge{Text: "dir", Tone: "accent"})
		} else if ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(res.FullPath)), "."); ext != "" {
			badges = append(badges, ui.Badge{Text: ext, Tone: "muted"})
		}
		icon := "•"
		if res.IsDir {
			icon = "▸"
		}
		items[i] = picker.Item{
			ID:       res.FullPath,
			Title:    icon + " " + res.Label,
			Subtitle: res.FullPath,
			Badges:   badges,
			Keywords: []string{res.FullPath, res.Label, filepath.Base(res.FullPath)},
			Data:     res,
		}
	}

	pm := picker.New(fmt.Sprintf("Search Results (%d)", len(results)), items)
	pm.Placeholder = "filter · enter open · o open with · y yank · a attach · r refine"

	m := a.newPickerModal(pm, modal.PickerOptions{
		ID:       fdResultsModalID,
		Scope:    fdResultsModalID,
		PageSize: 12,
		OnConfirm: func(picked picker.Item) modal.Result {
			res, ok := picked.Data.(fdResult)
			if !ok {
				return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
			}
			target := a.fdResultOpenTarget(res, attachTarget)
			a.afterModal(func(st core.State) core.State {
				return a.runHandler(st, target.rules[0], target)
			})
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
		OnCommand: func(p *picker.Model, cmd core.Command) (modal.Result, bool) {
			switch cmd.ID {
			case CmdFDResultRefine:
				a.afterModal(func(st core.State) core.State {
					return a.openFDSearchModal(st, core.Command{}, true)
				})
				return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}, true
			case CmdFDResultYank, CmdFDResultOpenWith, CmdFDResultAttach:
			default:
				return modal.Result{}, false
			}
			picked, ok := p.Focused()
			if !ok {
				return modal.Result{Handled: true, Status: "no file matches", SetStatus: true}, true
			}
			res, ok := picked.Data.(fdResult)
			if !ok {
				return modal.Result{Handled: true}, true
			}
			switch cmd.ID {
			case CmdFDResultYank:
				a.yank(res.FullPath, "path")
				return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}, true
			case CmdFDResultOpenWith:
				target := a.fdResultOpenTarget(res, attachTarget)
				a.afterModal(func(st core.State) core.State {
					return a.openWithTarget(st, target)
				})
				return modal.Result{Handled: true}, true
			case CmdFDResultAttach:
				kind := "file"
				if res.IsDir {
					kind = "path"
				}
				item := CollectedContextItem{
					Kind:   kind,
					Value:  res.FullPath,
					Path:   res.FullPath,
					Label:  res.Label,
					Source: "fd",
				}
				a.afterModal(func(st core.State) core.State {
					return a.attachContextItem(st, item, attachTarget)
				})
				return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}, true
			}
			return modal.Result{}, false
		},
	})
	if runErr != nil {
		a.pushToast(runErr.Error(), toast.Warn)
	}
	a.say("%d files matched · enter open · o open with · y yank · a attach · r refine", len(results))
	return a.pushModal(s, m)
}

func cloneFDValues(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	out := make(map[string]any, len(src))
	for k, v := range src {
		switch t := v.(type) {
		case []string:
			out[k] = slices.Clone(t)
		case []any:
			out[k] = slices.Clone(t)
		default:
			out[k] = v
		}
	}
	return out
}

func fdString(values map[string]any, key string) string {
	v, ok := values[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	default:
		return fmt.Sprint(t)
	}
}

func fdBool(values map[string]any, key string) bool {
	v, ok := values[key]
	if !ok || v == nil {
		return false
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return strings.EqualFold(strings.TrimSpace(t), "true")
	default:
		return false
	}
}

func fdInt(values map[string]any, key string) (int, error) {
	v, ok := values[key]
	if !ok || v == nil {
		return 0, nil
	}
	switch t := v.(type) {
	case int:
		return t, nil
	case int64:
		return int(t), nil
	case float64:
		return int(t), nil
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return 0, nil
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			return 0, fmt.Errorf("must be an integer")
		}
		return n, nil
	default:
		return 0, fmt.Errorf("must be an integer")
	}
}

func fdStrings(values map[string]any, key string) []string {
	v, ok := values[key]
	if !ok || v == nil {
		return nil
	}
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if item != nil {
				out = append(out, fmt.Sprint(item))
			}
		}
		return out
	case string:
		if strings.TrimSpace(t) == "" {
			return nil
		}
		return []string{t}
	default:
		return []string{fmt.Sprint(t)}
	}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if line, _, ok := strings.Cut(s, "\n"); ok {
		return strings.TrimSpace(line)
	}
	return s
}
