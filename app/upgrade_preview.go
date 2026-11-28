package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/markdown"
	"ofam.dev/ochazuke/widgets/modal"
)

// UpgradeBinaryInfo describes a running or on-disk mtm binary build.
type UpgradeBinaryInfo struct {
	Version         string
	Commit          string
	ShortSHA        string
	Dirty           bool
	BuiltAt         string
	OchazukeVersion string
	OdenVersion     string
	Path            string
	ModTime         time.Time
	SizeBytes       int64
	ProbeErr        string
}

// UpgradePreviewInfo summarizes what pressing g U will reload into.
type UpgradePreviewInfo struct {
	Running       UpgradeBinaryInfo
	Installed     UpgradeBinaryInfo
	DaemonRunning bool
	DaemonVersion string
	DaemonCommit  string
	DaemonMatch   bool
	HasUpgrade    bool
	StatusSummary string
	RepoDir       string
	Commits       []string
	Changelog     string
}

// upgradePreviewBuilder is overridable in tests so upgrade preview tests stay
// deterministic and do not depend on host ~/bin/mtm state.
var upgradePreviewBuilder = buildDefaultUpgradePreview

func (a *App) upgradePreview(s core.State, _ core.Command) core.StepResult {
	s = a.dropModals(s)
	info := upgradePreviewBuilder(a)
	md := markdown.New("Pending g U Upgrade Preview", FormatUpgradePreviewMarkdown(info))
	md.ShowTOC = len(md.Headings) > 1

	badgeTone := "ok"
	badgeText := "up to date"
	if info.HasUpgrade {
		badgeTone = "accent"
		badgeText = "upgrade ready"
	} else if info.Installed.ProbeErr != "" {
		badgeTone = "warn"
		badgeText = "binary check failed"
	}

	m := modal.NewMarkdownModal(md, modal.MarkdownOptions{
		ID:              upgradePreviewModalID,
		Scope:           "about",
		Mode:            ModeAbout,
		FocusID:         "upgrade.preview.md",
		WidthPct:        86,
		HeightPct:       84,
		ShowStatsBadges: false,
		Badges: func(_ *markdown.Model, _ string, _ int) []ui.Badge {
			return []ui.Badge{
				{Text: badgeText, Tone: badgeTone},
				{Text: "g U", Tone: "muted"},
			}
		},
	})
	a.say("upgrade preview · close with esc/q, then press g U to reload")
	return core.Next(a.pushModal(s, m))
}

func buildDefaultUpgradePreview(a *App) UpgradePreviewInfo {
	cur := daemon.CurrentBuildInfo()
	running := UpgradeBinaryInfo{
		Version:         cur.Version,
		Commit:          cur.Commit,
		ShortSHA:        cur.ShortSHA,
		Dirty:           cur.Dirty,
		BuiltAt:         cur.BuiltAt,
		OchazukeVersion: cur.OchazukeVersion,
		OdenVersion:     cur.OdenVersion,
		Path:            cur.Binary,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	installedPath := resolveInstalledMTMBinary()
	installed := probeInstalledMTMBinary(ctx, installedPath)

	info := UpgradePreviewInfo{
		Running:   running,
		Installed: installed,
	}
	fillDaemonUpgradeStatus(ctx, a, cur, &info)
	info.HasUpgrade, info.StatusSummary = evaluateUpgradeDelta(running, installed)

	if repoDir := findMatomeruRepoDir(a); repoDir != "" {
		info.RepoDir = repoDir
		info.Commits = readUpgradeCommitDelta(ctx, repoDir, running.Commit, installed.Commit)
		info.Changelog = readChangelogPreview(repoDir)
	}
	return info
}

func fillDaemonUpgradeStatus(ctx context.Context, a *App, cur daemon.BuildInfo, info *UpgradePreviewInfo) {
	if a == nil {
		return
	}
	client := a.daemonClient()
	if client == nil {
		return
	}
	st, err := client.Status(ctx)
	if err != nil || st == nil {
		return
	}
	info.DaemonRunning = true
	info.DaemonVersion = st.Version
	info.DaemonCommit = shortCommit(st.Commit)
	info.DaemonMatch = cur.SameBuild(st)
}

func resolveInstalledMTMBinary() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidate := filepath.Join(home, "bin", "mtm")
		if fi, statErr := os.Stat(candidate); statErr == nil && !fi.IsDir() {
			return candidate
		}
	}
	if p, err := exec.LookPath("mtm"); err == nil && p != "" {
		return p
	}
	if len(os.Args) > 0 && strings.TrimSpace(os.Args[0]) != "" {
		if p, err := exec.LookPath(os.Args[0]); err == nil && p != "" {
			return p
		}
	}
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return ""
}

func probeInstalledMTMBinary(ctx context.Context, binPath string) UpgradeBinaryInfo {
	out := UpgradeBinaryInfo{Path: binPath}
	if strings.TrimSpace(binPath) == "" {
		out.ProbeErr = "could not locate installed mtm binary (checked ~/bin/mtm and PATH)"
		return out
	}
	if fi, err := os.Stat(binPath); err == nil {
		out.ModTime = fi.ModTime().UTC()
		out.SizeBytes = fi.Size()
	} else {
		out.ProbeErr = err.Error()
		return out
	}
	if parsed, ok := probeMTMVersionJSON(ctx, binPath); ok {
		parsed.Path = binPath
		parsed.ModTime = out.ModTime
		parsed.SizeBytes = out.SizeBytes
		return parsed
	}
	if ver, err := probeMTMVersionText(ctx, binPath); err == nil && ver != "" {
		out.Version = ver
		return out
	} else if err != nil {
		out.ProbeErr = err.Error()
	}
	return out
}

func probeMTMVersionJSON(ctx context.Context, binPath string) (UpgradeBinaryInfo, bool) {
	raw, err := exec.CommandContext(ctx, binPath, "version", "--json").Output()
	if err != nil {
		return UpgradeBinaryInfo{}, false
	}
	var parsed struct {
		Version         string `json:"version"`
		Commit          string `json:"commit"`
		ShortSHA        string `json:"shortSha"`
		Dirty           bool   `json:"dirty"`
		BuiltAt         string `json:"builtAt"`
		OchazukeVersion string `json:"ochazukeVersion"`
		OdenVersion     string `json:"odenVersion"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil || strings.TrimSpace(parsed.Version) == "" {
		return UpgradeBinaryInfo{}, false
	}
	short := parsed.ShortSHA
	if short == "" {
		short = shortCommit(parsed.Commit)
	}
	return UpgradeBinaryInfo{
		Version:         parsed.Version,
		Commit:          parsed.Commit,
		ShortSHA:        short,
		Dirty:           parsed.Dirty,
		BuiltAt:         parsed.BuiltAt,
		OchazukeVersion: parsed.OchazukeVersion,
		OdenVersion:     parsed.OdenVersion,
	}, true
}

func probeMTMVersionText(ctx context.Context, binPath string) (string, error) {
	raw, err := exec.CommandContext(ctx, binPath, "--version").Output()
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(string(raw))
	line = strings.TrimPrefix(line, "mtm ")
	return strings.TrimSpace(line), nil
}

func evaluateUpgradeDelta(running, installed UpgradeBinaryInfo) (bool, string) {
	if installed.ProbeErr != "" {
		return false, "Could not inspect installed binary: " + installed.ProbeErr
	}
	runVer := formatBuildTag(running)
	instVer := formatBuildTag(installed)
	if running.Version != "" && installed.Version != "" && running.Version != installed.Version {
		return true, fmt.Sprintf("Upgrade ready: %s -> %s", runVer, instVer)
	}
	if commitsDiffer(running.Commit, installed.Commit) {
		return true, fmt.Sprintf("Newer build installed: %s -> %s", runVer, instVer)
	}
	if running.BuiltAt != "" && installed.BuiltAt != "" && running.BuiltAt != installed.BuiltAt {
		return true, fmt.Sprintf("Rebuilt binary installed (%s -> %s)", running.BuiltAt, installed.BuiltAt)
	}
	return false, fmt.Sprintf("Running binary already matches installed %s", instVer)
}

func commitsDiffer(a, b string) bool {
	sa, sb := shortCommit(a), shortCommit(b)
	return sa != "" && sb != "" && sa != sb
}

func shortCommit(c string) string {
	c = strings.TrimSpace(c)
	if len(c) > 7 {
		return c[:7]
	}
	return c
}

func formatBuildTag(b UpgradeBinaryInfo) string {
	ver := strings.TrimSpace(b.Version)
	if ver == "" {
		ver = "unknown"
	}
	sha := b.ShortSHA
	if sha == "" {
		sha = shortCommit(b.Commit)
	}
	if sha != "" {
		ver += " (" + sha + ")"
	}
	if b.Dirty {
		ver += " [dirty]"
	}
	return ver
}

func findMatomeruRepoDir(a *App) string {
	var candidates []string
	if a != nil && a.cfg != nil && a.cfg.Settings != nil {
		if configured := strings.TrimSpace(a.cfg.Settings.String(SetSelfHostedFeaturesRepoPath, "")); configured != "" {
			candidates = append(candidates, expandHome(configured))
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates, filepath.Join(home, "proj", "matomeru"))
	}
	if cwd, err := os.Getwd(); err == nil && cwd != "" {
		candidates = append(candidates, cwd)
	}
	for _, dir := range candidates {
		if isMatomeruGitRepo(dir) {
			return dir
		}
	}
	return ""
}

func isMatomeruGitRepo(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return false
	}
	raw, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return false
	}
	return strings.Contains(string(raw), "module ofam.dev/matomeru")
}

func readUpgradeCommitDelta(ctx context.Context, repoDir, fromCommit, toCommit string) []string {
	var args []string
	switch {
	case fromCommit != "" && toCommit != "" && commitsDiffer(fromCommit, toCommit):
		args = []string{"-C", repoDir, "log", "--oneline", "-n", "25", fromCommit + ".." + toCommit}
	case fromCommit != "":
		args = []string{"-C", repoDir, "log", "--oneline", "-n", "15", fromCommit + "..HEAD"}
	default:
		args = []string{"-C", repoDir, "log", "--oneline", "-n", "10"}
	}
	raw, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	var out []string
	for _, line := range lines {
		if s := strings.TrimSpace(line); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func readChangelogPreview(repoDir string) string {
	raw, err := os.ReadFile(filepath.Join(repoDir, "CHANGELOG.md"))
	if err != nil {
		return ""
	}
	lines := strings.Split(string(raw), "\n")
	var out []string
	for _, line := range lines {
		out = append(out, line)
		if len(out) >= 24 {
			break
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// FormatUpgradePreviewMarkdown renders UpgradePreviewInfo into a clean
// Markdown report shown inside the upgrade preview modal.
func FormatUpgradePreviewMarkdown(info UpgradePreviewInfo) string {
	var b strings.Builder
	b.WriteString("# Pending `g U` Upgrade Preview\n\n")
	fmt.Fprintf(&b, "**Status:** %s\n\n", info.StatusSummary)

	b.WriteString("## Binary Comparison\n\n")
	fmt.Fprintf(&b, "- **Running client:** `%s`", formatBuildTag(info.Running))
	if info.Running.BuiltAt != "" {
		fmt.Fprintf(&b, " · built `%s`", info.Running.BuiltAt)
	}
	b.WriteString("\n")

	if info.Installed.ProbeErr != "" {
		fmt.Fprintf(&b, "- **Installed binary:** error (%s)\n", info.Installed.ProbeErr)
	} else {
		fmt.Fprintf(&b, "- **Installed binary:** `%s`", formatBuildTag(info.Installed))
		if info.Installed.BuiltAt != "" {
			fmt.Fprintf(&b, " · built `%s`", info.Installed.BuiltAt)
		}
		if info.Installed.Path != "" {
			fmt.Fprintf(&b, " · `%s`", info.Installed.Path)
		}
		b.WriteString("\n")
	}

	writeDepComparison(&b, "ochazuke", info.Running.OchazukeVersion, info.Installed.OchazukeVersion)
	writeDepComparison(&b, "oden", info.Running.OdenVersion, info.Installed.OdenVersion)
	writeDaemonComparison(&b, info)
	b.WriteString("\n")

	if len(info.Commits) > 0 {
		b.WriteString("## Recent Commits / Delta\n\n")
		for _, c := range info.Commits {
			fmt.Fprintf(&b, "- `%s`\n", c)
		}
		b.WriteString("\n")
	}

	if info.Changelog != "" {
		b.WriteString("## Changelog Excerpt\n\n")
		b.WriteString(info.Changelog)
		b.WriteString("\n\n")
	}

	b.WriteString("## How to Apply\n\n")
	b.WriteString("- Close this preview with `esc` or `q`, then press **`g U`** to reload the TUI client binary in place without disturbing live panes.\n")
	b.WriteString("- Press **`g D`** to upgrade the background daemon in place.\n")
	return b.String()
}

func writeDepComparison(b *strings.Builder, name, running, installed string) {
	if running == "" && installed == "" {
		return
	}
	if running == installed {
		fmt.Fprintf(b, "- **%s:** `%s` (unchanged)\n", name, running)
		return
	}
	fmt.Fprintf(b, "- **%s:** `%s` -> `%s`\n", name, emptyAsDash(running), emptyAsDash(installed))
}

func writeDaemonComparison(b *strings.Builder, info UpgradePreviewInfo) {
	if !info.DaemonRunning {
		b.WriteString("- **Daemon:** not connected\n")
		return
	}
	state := "matches running client"
	if !info.DaemonMatch {
		state = "differs from running client (run `g D` to upgrade daemon)"
	}
	fmt.Fprintf(b, "- **Daemon:** `%s` (`%s`) · %s\n", emptyAsDash(info.DaemonVersion), emptyAsDash(info.DaemonCommit), state)
}

func emptyAsDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}
