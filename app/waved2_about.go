package app

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/harness"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/markdown"
)

// AboutBuildInfo summarizes build metadata and key Go module dependencies.
type AboutBuildInfo struct {
	Version         string `json:"version"`
	Commit          string `json:"commit,omitempty"`
	GoVersion       string `json:"goVersion"`
	OchazukeVersion string `json:"ochazukeVersion,omitempty"`
	OdenVersion     string `json:"odenVersion,omitempty"`
	MTMHookPath     string `json:"mtmHookPath,omitempty"`
}

// AboutPaneInfo summarizes a live or stored pane's identity and process status.
type AboutPaneInfo struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Harness  string `json:"harness,omitempty"`
	Status   string `json:"status"`
	PID      int    `json:"pid,omitempty"`
	Task     string `json:"task,omitempty"`
	Unlinked bool   `json:"unlinked,omitempty"`
}

// AboutProcessInfo summarizes the current mtm process, local daemon, and panes.
type AboutProcessInfo struct {
	PID          int             `json:"pid"`
	Executable   string          `json:"executable,omitempty"`
	DaemonStatus string          `json:"daemonStatus"`
	DaemonPID    int             `json:"daemonPid,omitempty"`
	DaemonSocket string          `json:"daemonSocket,omitempty"`
	DaemonMCPURL string          `json:"daemonMcpUrl,omitempty"`
	DaemonUptime string          `json:"daemonUptime,omitempty"`
	Panes        []AboutPaneInfo `json:"panes"`
}

// AboutConfigFile reports presence and size of one Matomeru config file.
type AboutConfigFile struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Loaded    bool   `json:"loaded"`
	SizeBytes int64  `json:"sizeBytes,omitempty"`
}

// AboutStorageInfo summarizes database, managed files, config files, and runtime socket.
type AboutStorageInfo struct {
	DBPath           string            `json:"dbPath"`
	DBSizeBytes      int64             `json:"dbSizeBytes"`
	RecordCounts     map[string]int    `json:"recordCounts"`
	TotalRecords     int               `json:"totalRecords"`
	ManagedFilesDir  string            `json:"managedFilesDir,omitempty"`
	ManagedFileCount int               `json:"managedFileCount"`
	ConfigDir        string            `json:"configDir"`
	ConfigFiles      []AboutConfigFile `json:"configFiles"`
	RuntimeSocket    string            `json:"runtimeSocket"`
}

// AboutExtensionInfo summarizes one discovered extension and its mounted nouns.
type AboutExtensionInfo struct {
	Name    string   `json:"name"`
	Version string   `json:"version,omitempty"`
	Binary  string   `json:"binary,omitempty"`
	Status  string   `json:"status,omitempty"`
	Nouns   []string `json:"nouns,omitempty"`
}

// AboutHarnessInfo summarizes one detected or configured agent harness.
type AboutHarnessInfo struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Command    string `json:"command"`
	Source     string `json:"source"`
	Executable string `json:"executable,omitempty"`
	Installed  bool   `json:"installed"`
}

// AboutInfo is the complete runtime, build, storage, extension, and harness
// diagnostic snapshot rendered by `app.about` (g A) and `mtm about`.
type AboutInfo struct {
	Build      AboutBuildInfo       `json:"build"`
	Processes  AboutProcessInfo     `json:"processes"`
	Storage    AboutStorageInfo     `json:"storage"`
	Extensions []AboutExtensionInfo `json:"extensions"`
	Harnesses  []AboutHarnessInfo   `json:"harnesses"`
}

var aboutConfigFileNames = []string{
	"settings.json",
	"bindings.json",
	"themes.json",
	"handlers.json",
	"harnesses.json",
	"actions.json",
	"drafts.json",
}

func resolveConfigDir(explicit string) string {
	if strings.TrimSpace(explicit) != "" {
		return explicit
	}
	if env := strings.TrimSpace(os.Getenv("MTM_CONFIG_DIR")); env != "" {
		return env
	}
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, "matomeru")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".config", "matomeru")
	}
	return filepath.Join(home, ".config", "matomeru")
}

func resolveMTMHookPath(exePath string) string {
	if exePath != "" {
		cand := filepath.Join(filepath.Dir(exePath), "mtm-hook")
		if info, err := os.Stat(cand); err == nil && !info.IsDir() {
			return cand
		}
	}
	if p, err := exec.LookPath("mtm-hook"); err == nil {
		return p
	}
	return ""
}

func dbDiskSize(dbPath string) int64 {
	if dbPath == "" || dbPath == ":memory:" || strings.Contains(dbPath, "mode=memory") {
		return 0
	}
	p := dbPath
	if strings.HasPrefix(p, "file:") {
		p = strings.TrimPrefix(p, "file:")
		if base, _, ok := strings.Cut(p, "?"); ok {
			p = base
		}
	}
	var total int64
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if info, err := os.Stat(p + suffix); err == nil && !info.IsDir() {
			total += info.Size()
		}
	}
	return total
}

func countFilesUnder(dir string) int {
	if strings.TrimSpace(dir) == "" {
		return 0
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return 0
	}
	count := 0
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			count++
		}
		return nil
	})
	return count
}

func humanBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	default:
		return fmt.Sprintf("%.2f GB", float64(n)/(1024*1024*1024))
	}
}

// BuildAboutInfo gathers live build, process/daemon, storage, extension, and
// harness diagnostics for both the TUI About overlay and the `mtm about` CLI.
func BuildAboutInfo(store *data.Store, configDir string, client *daemon.Client, harnesses []harness.Entry) AboutInfo {
	return buildAboutInfoWithCatalogs(store, configDir, client, harnesses, nil)
}

// BuildAboutInfo gathers AboutInfo using this App's store, daemon, mounted
// extension catalogs, config directory, and discovered harnesses.
func (a *App) BuildAboutInfo() AboutInfo {
	return buildAboutInfoWithCatalogs(a.Store, a.opts.ConfigDir, a.daemonClient(), a.Harnesses(), a.extCatalogs)
}

func buildAboutInfoWithCatalogs(store *data.Store, configDir string, client *daemon.Client, harnesses []harness.Entry, extCatalogs map[string]string) AboutInfo {
	exe, _ := os.Executable()
	build := AboutBuildInfo{
		Version:     "v2-dev",
		GoVersion:   runtime.Version(),
		MTMHookPath: resolveMTMHookPath(exe),
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi != nil {
		if bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			build.Version = bi.Main.Version
		}
		if bi.GoVersion != "" {
			build.GoVersion = bi.GoVersion
		}
		for _, dep := range bi.Deps {
			switch dep.Path {
			case "ofam.dev/ochazuke":
				build.OchazukeVersion = dep.Version
				if dep.Replace != nil && dep.Replace.Version != "" {
					build.OchazukeVersion = dep.Replace.Version
				} else if dep.Replace != nil && dep.Replace.Path != "" {
					build.OchazukeVersion = dep.Version + " (local)"
				}
			case "ofam.dev/oden":
				build.OdenVersion = dep.Version
				if dep.Replace != nil && dep.Replace.Version != "" {
					build.OdenVersion = dep.Replace.Version
				} else if dep.Replace != nil && dep.Replace.Path != "" {
					build.OdenVersion = dep.Version + " (local)"
				}
			}
		}
		for _, setting := range bi.Settings {
			if setting.Key == "vcs.revision" && setting.Value != "" {
				rev := setting.Value
				if len(rev) > 12 {
					rev = rev[:12]
				}
				build.Commit = rev
			}
		}
	}

	sockPath := daemon.ResolveSocketPath("")
	if client != nil && client.SocketPath() != "" {
		sockPath = client.SocketPath()
	}

	proc := AboutProcessInfo{
		PID:          os.Getpid(),
		Executable:   exe,
		DaemonStatus: "stopped",
		DaemonSocket: sockPath,
		Panes:        []AboutPaneInfo{},
	}

	liveByID := map[string]daemon.PaneInfo{}
	if client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), daemonRPCTimeout)
		defer cancel()
		if st, err := client.Status(ctx); err == nil && st != nil {
			proc.DaemonStatus = "running"
			proc.DaemonPID = st.PID
			if st.Socket != "" {
				proc.DaemonSocket = st.Socket
				sockPath = st.Socket
			}
			proc.DaemonMCPURL = st.MCPURL
			proc.DaemonUptime = st.Uptime
		}
		if livePanes, err := client.List(ctx); err == nil {
			for _, lp := range livePanes {
				liveByID[lp.ID] = lp
			}
		}
	}

	seenPanes := map[string]bool{}
	if store != nil {
		paneSchema, _ := store.Registry().Get(data.KindPane)
		taskSchema, _ := store.Registry().Get(data.KindTask)
		if res, err := store.Query(data.Query{Kind: data.KindPane}); err == nil {
			for _, p := range res.Items {
				seenPanes[p.ID] = true
				pid := recordIntField(p, "pid")
				status := p.Text("runStatus")
				if lp, alive := liveByID[p.ID]; alive {
					if lp.PID > 0 {
						pid = lp.PID
					}
					if lp.Exited {
						status = "exited"
					} else if status == "" || status == "unknown" {
						status = "running"
					}
				}
				taskTitle := ""
				taskRef, hasTask := p.RefField("task")
				if hasTask {
					if t, err := store.Get(taskRef); err == nil && t != nil {
						taskTitle = views.Title(taskSchema, *t)
					} else {
						taskTitle = taskRef.ID
					}
				}
				proc.Panes = append(proc.Panes, AboutPaneInfo{
					ID:       p.ID,
					Title:    views.Title(paneSchema, p),
					Harness:  p.Text("harness"),
					Status:   status,
					PID:      pid,
					Task:     taskTitle,
					Unlinked: !hasTask,
				})
			}
		}
	}
	// Include any live daemon panes not yet in the store.
	for id, lp := range liveByID {
		if seenPanes[id] {
			continue
		}
		status := "running"
		if lp.Exited {
			status = "exited"
		}
		title := lp.Title
		if title == "" {
			title = lp.Cmd
		}
		if title == "" {
			title = lp.ID
		}
		proc.Panes = append(proc.Panes, AboutPaneInfo{
			ID:       lp.ID,
			Title:    title,
			Status:   status,
			PID:      lp.PID,
			Unlinked: true,
		})
	}
	slices.SortStableFunc(proc.Panes, func(x, y AboutPaneInfo) int { return strings.Compare(x.ID, y.ID) })

	// Storage & paths.
	cfgDir := resolveConfigDir(configDir)
	cfgFiles := make([]AboutConfigFile, 0, len(aboutConfigFileNames))
	for _, name := range aboutConfigFileNames {
		p := filepath.Join(cfgDir, name)
		cf := AboutConfigFile{Name: name, Path: p}
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			cf.Loaded = true
			cf.SizeBytes = info.Size()
		}
		cfgFiles = append(cfgFiles, cf)
	}

	storage := AboutStorageInfo{
		RecordCounts:  map[string]int{},
		ConfigDir:     cfgDir,
		ConfigFiles:   cfgFiles,
		RuntimeSocket: sockPath,
	}
	if store != nil {
		storage.DBPath = store.DBPath()
		storage.DBSizeBytes = dbDiskSize(storage.DBPath)
		storage.ManagedFilesDir = store.FilesRoot()
		storage.ManagedFileCount = countFilesUnder(storage.ManagedFilesDir)
		for _, sc := range store.Registry().List() {
			if res, err := store.Query(data.Query{Kind: sc.Kind, Limit: 1}); err == nil {
				storage.RecordCounts[sc.Kind] = res.Total
				storage.TotalRecords += res.Total
			}
		}
	}

	// Extensions & mounted nouns.
	nounsByBinary := map[string][]string{}
	for kind, bin := range extCatalogs {
		if bin == "" {
			continue
		}
		nounsByBinary[bin] = append(nounsByBinary[bin], kind)
		nounsByBinary[filepath.Base(bin)] = append(nounsByBinary[filepath.Base(bin)], kind)
	}
	var exts []AboutExtensionInfo
	if store != nil {
		if res, err := store.Query(data.Query{Kind: data.KindExtension}); err == nil {
			for _, rec := range res.Items {
				bin := rec.Text("binary")
				name := rec.Text("name")
				nouns := slices.Clone(nounsByBinary[bin])
				if len(nouns) == 0 && bin != "" {
					nouns = slices.Clone(nounsByBinary[filepath.Base(bin)])
				}
				if len(nouns) == 0 && name != "" {
					nouns = slices.Clone(nounsByBinary[name])
				}
				slices.Sort(nouns)
				exts = append(exts, AboutExtensionInfo{
					Name:    name,
					Version: rec.Text("version"),
					Binary:  bin,
					Status:  rec.Text("runStatus"),
					Nouns:   slices.Compact(nouns),
				})
			}
		}
	}
	slices.SortStableFunc(exts, func(x, y AboutExtensionInfo) int { return strings.Compare(x.Name, y.Name) })

	// Harnesses.
	hList := make([]AboutHarnessInfo, 0, len(harnesses))
	for _, h := range harnesses {
		hList = append(hList, AboutHarnessInfo{
			ID:         h.ID,
			Label:      h.Label,
			Command:    shellJoin(h.Command),
			Source:     h.Source,
			Executable: h.Executable,
			Installed:  h.Installed,
		})
	}

	return AboutInfo{
		Build:      build,
		Processes:  proc,
		Storage:    storage,
		Extensions: exts,
		Harnesses:  hList,
	}
}

// FormatAboutMarkdown renders AboutInfo as clean, structured Markdown for the
// TUI About overlay and readable CLI display.
func FormatAboutMarkdown(info AboutInfo) string {
	var b strings.Builder
	b.WriteString("# About Matomeru\n\n")

	b.WriteString("## Build & Versions\n\n")
	fmt.Fprintf(&b, "- **mtm version:** `%s`", info.Build.Version)
	if info.Build.Commit != "" {
		fmt.Fprintf(&b, " (commit `%s`)", info.Build.Commit)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "- **Go:** `%s`\n", info.Build.GoVersion)
	if info.Build.OchazukeVersion != "" {
		fmt.Fprintf(&b, "- **ochazuke:** `%s`\n", info.Build.OchazukeVersion)
	}
	if info.Build.OdenVersion != "" {
		fmt.Fprintf(&b, "- **oden:** `%s`\n", info.Build.OdenVersion)
	}
	hookPath := info.Build.MTMHookPath
	if hookPath == "" {
		hookPath = "(not found in PATH)"
	}
	fmt.Fprintf(&b, "- **mtm-hook:** `%s`\n\n", hookPath)

	b.WriteString("## Processes & Daemon\n\n")
	fmt.Fprintf(&b, "- **mtm PID:** `%d`", info.Processes.PID)
	if info.Processes.Executable != "" {
		fmt.Fprintf(&b, " (`%s`)", info.Processes.Executable)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "- **Daemon:** `%s`", info.Processes.DaemonStatus)
	if info.Processes.DaemonPID > 0 {
		fmt.Fprintf(&b, " · PID `%d`", info.Processes.DaemonPID)
	}
	if info.Processes.DaemonUptime != "" {
		fmt.Fprintf(&b, " · uptime %s", info.Processes.DaemonUptime)
	}
	b.WriteString("\n")
	if info.Processes.DaemonSocket != "" {
		fmt.Fprintf(&b, "- **Socket:** `%s`\n", info.Processes.DaemonSocket)
	}
	if info.Processes.DaemonMCPURL != "" {
		fmt.Fprintf(&b, "- **HTTP MCP URL:** `%s`\n", info.Processes.DaemonMCPURL)
	}
	fmt.Fprintf(&b, "- **Panes (%d):**\n", len(info.Processes.Panes))
	if len(info.Processes.Panes) == 0 {
		b.WriteString("  - *(no active panes)*\n")
	} else {
		for _, p := range info.Processes.Panes {
			parts := []string{fmt.Sprintf("`%s` **%s**", p.ID, p.Title)}
			if p.Harness != "" {
				parts = append(parts, p.Harness)
			}
			if p.Status != "" {
				parts = append(parts, p.Status)
			}
			if p.PID > 0 {
				parts = append(parts, fmt.Sprintf("pid %d", p.PID))
			}
			if p.Unlinked {
				parts = append(parts, "[unlinked]")
			} else if p.Task != "" {
				parts = append(parts, "task: "+p.Task)
			}
			fmt.Fprintf(&b, "  - %s\n", strings.Join(parts, " · "))
		}
	}
	b.WriteString("\n")

	b.WriteString("## Storage & Paths\n\n")
	dbSize := ""
	if info.Storage.DBSizeBytes > 0 {
		dbSize = " (" + humanBytes(info.Storage.DBSizeBytes) + ")"
	}
	fmt.Fprintf(&b, "- **SQLite DB:** `%s`%s · %d records\n", info.Storage.DBPath, dbSize, info.Storage.TotalRecords)
	if len(info.Storage.RecordCounts) > 0 {
		kinds := make([]string, 0, len(info.Storage.RecordCounts))
		for k := range info.Storage.RecordCounts {
			kinds = append(kinds, k)
		}
		slices.Sort(kinds)
		var countParts []string
		for _, k := range kinds {
			if n := info.Storage.RecordCounts[k]; n > 0 {
				countParts = append(countParts, fmt.Sprintf("%s=%d", k, n))
			}
		}
		if len(countParts) > 0 {
			fmt.Fprintf(&b, "  - **Counts:** %s\n", strings.Join(countParts, ", "))
		}
	}
	if info.Storage.ManagedFilesDir != "" {
		fmt.Fprintf(&b, "- **Managed files:** `%s` (%d files)\n", info.Storage.ManagedFilesDir, info.Storage.ManagedFileCount)
	}
	fmt.Fprintf(&b, "- **Runtime socket:** `%s`\n", info.Storage.RuntimeSocket)
	fmt.Fprintf(&b, "- **Config dir:** `%s`\n", info.Storage.ConfigDir)
	for _, cf := range info.Storage.ConfigFiles {
		state := "missing"
		if cf.Loaded {
			state = fmt.Sprintf("loaded (%s)", humanBytes(cf.SizeBytes))
		}
		fmt.Fprintf(&b, "  - `%s`: %s\n", cf.Name, state)
	}
	b.WriteString("\n")

	b.WriteString("## Extensions\n\n")
	if len(info.Extensions) == 0 {
		b.WriteString("- *(no extensions discovered)*\n\n")
	} else {
		for _, e := range info.Extensions {
			line := fmt.Sprintf("- **%s**", e.Name)
			if e.Version != "" {
				line += fmt.Sprintf(" `%s`", e.Version)
			}
			if e.Status != "" {
				line += " · " + e.Status
			}
			if e.Binary != "" {
				line += fmt.Sprintf(" · `%s`", e.Binary)
			}
			if len(e.Nouns) > 0 {
				line += " · nouns: " + strings.Join(e.Nouns, ", ")
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("## Harnesses\n\n")
	if len(info.Harnesses) == 0 {
		b.WriteString("- *(no harnesses detected)*\n")
	} else {
		for _, h := range info.Harnesses {
			line := fmt.Sprintf("- **%s** (`%s`)", h.Label, h.ID)
			if h.Command != "" {
				line += fmt.Sprintf(" · cmd `%s`", h.Command)
			}
			if h.Source != "" {
				line += " · " + h.Source
			}
			if h.Executable != "" {
				line += fmt.Sprintf(" · `%s`", h.Executable)
			}
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

// aboutOpen opens the scrollable About diagnostics overlay (`app.about`, g A).
func (a *App) aboutOpen(s core.State, _ core.Command) core.StepResult {
	s = a.dropModals(s)
	info := a.BuildAboutInfo()
	md := markdown.New("About Matomeru", FormatAboutMarkdown(info))
	md.ShowTOC = true
	a.aboutMD = md
	a.say("about matomeru · j/k scroll · esc/q closes")
	return core.Next(openModal(s, modalAbout, ModeAbout, modalAbout+".md"))
}

func (a *App) aboutModal() ui.ViewNode {
	md := a.aboutMD
	if md == nil {
		return overlay(modalAbout, "About Matomeru", 86, 86, text(modalAbout+".md", "About info unavailable.", "muted"))
	}
	if a.Shell != nil && a.Shell.H > 12 {
		md.PageSize = a.Shell.H*85/100 - 6
	}
	body := md.View(modalAbout + ".md")
	ov := overlay(modalAbout, "About Matomeru", 88, 88, body)
	ov.Scope = "about"
	ov.Badges = []ui.Badge{{Text: "diagnostics", Tone: "accent"}, {Text: "g A", Tone: "muted"}}
	return ov
}
