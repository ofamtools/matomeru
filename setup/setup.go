// Package setup configures a developer machine for the OFAM toolchain:
// agent instruction files (AGENTS.md / GEMINI.md / CLAUDE.md), shared skills,
// and Claude Code hook integration. It uses only the Go standard library.
package setup

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//go:embed assets/ofam-rules.md
var embeddedOFAMRules string

//go:embed assets/skills/ofam-wave-release/SKILL.md
var embeddedSkillsFS embed.FS

// Action status values reported by machine setup and check operations.
const (
	StatusCreated     = "created"
	StatusUpdated     = "updated"
	StatusLinked      = "linked"
	StatusUnchanged   = "unchanged"
	StatusSkipped     = "skipped"
	StatusConflict    = "conflict"
	StatusWouldCreate = "would_create"
	StatusWouldUpdate = "would_update"
	StatusWouldLink   = "would_link"
	StatusMissing     = "missing"
	StatusOutdated    = "outdated"
)

// Options controls machine setup, dry-run preview, and drift checks.
type Options struct {
	Home      string
	Source    string
	SkillsSrc []string
	HookCmd   string
	DryRun    bool
	Check     bool
	Force     bool
	Now       func() time.Time
}

// Action describes the planned, checked, or applied state of one setup target.
type Action struct {
	Component string `json:"component"`
	Target    string `json:"target"`
	Path      string `json:"path"`
	Status    string `json:"status"`
	Backup    string `json:"backup,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

// Report aggregates all setup actions and whether the machine is converged.
type Report struct {
	OK      bool     `json:"ok"`
	DryRun  bool     `json:"dryRun,omitempty"`
	Check   bool     `json:"check,omitempty"`
	Actions []Action `json:"actions"`
}

// OFAMRulesBlock returns the embedded canonical OFAM managed rules block.
func OFAMRulesBlock() string {
	return strings.TrimSpace(embeddedOFAMRules)
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

func resolveHome(home string) (string, error) {
	if strings.TrimSpace(home) != "" {
		return filepath.Clean(home), nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return h, nil
}

func reportOK(actions []Action) bool {
	for _, a := range actions {
		switch a.Status {
		case StatusMissing, StatusOutdated, StatusConflict:
			return false
		}
	}
	return true
}

// RunMachine executes all machine setup steps (agents, skills, Claude hooks).
func RunMachine(opts Options) (Report, error) {
	rep := Report{
		DryRun:  opts.DryRun,
		Check:   opts.Check,
		Actions: []Action{},
	}

	agentActions, err := ConfigureAgents(opts)
	rep.Actions = append(rep.Actions, agentActions...)
	if err != nil {
		return rep, err
	}

	skillActions, err := SyncSkills(opts)
	rep.Actions = append(rep.Actions, skillActions...)
	if err != nil {
		return rep, err
	}

	hookActions, err := ConfigureClaudeHooks(opts)
	rep.Actions = append(rep.Actions, hookActions...)
	if err != nil {
		return rep, err
	}

	rep.OK = reportOK(rep.Actions)
	return rep, nil
}

// CheckMachine runs a read-only drift check across all machine setup components.
func CheckMachine(opts Options) (Report, error) {
	opts.Check = true
	opts.DryRun = false
	return RunMachine(opts)
}

// RunAgents runs only the agent instruction configurator and returns a Report.
func RunAgents(opts Options) (Report, error) {
	actions, err := ConfigureAgents(opts)
	if actions == nil {
		actions = []Action{}
	}
	rep := Report{
		OK:      err == nil && reportOK(actions),
		DryRun:  opts.DryRun,
		Check:   opts.Check,
		Actions: actions,
	}
	return rep, err
}

// RunSkills runs only the skills synchronizer and returns a Report.
func RunSkills(opts Options) (Report, error) {
	actions, err := SyncSkills(opts)
	if actions == nil {
		actions = []Action{}
	}
	rep := Report{
		OK:      err == nil && reportOK(actions),
		DryRun:  opts.DryRun,
		Check:   opts.Check,
		Actions: actions,
	}
	return rep, err
}

// RunHooks runs only the Claude Code hook configurator and returns a Report.
func RunHooks(opts Options) (Report, error) {
	actions, err := ConfigureClaudeHooks(opts)
	if actions == nil {
		actions = []Action{}
	}
	rep := Report{
		OK:      err == nil && reportOK(actions),
		DryRun:  opts.DryRun,
		Check:   opts.Check,
		Actions: actions,
	}
	return rep, err
}

func backupPathFor(path string, now time.Time) string {
	base := path + ".bak-" + now.Format("20060102")
	if _, err := os.Lstat(base); os.IsNotExist(err) {
		return base
	}
	ts := path + ".bak-" + now.Format("20060102-150405")
	if _, err := os.Lstat(ts); os.IsNotExist(err) {
		return ts
	}
	for i := 1; ; i++ {
		cand := fmt.Sprintf("%s-%d", ts, i)
		if _, err := os.Lstat(cand); os.IsNotExist(err) {
			return cand
		}
	}
}

// atomicWriteWithBackup creates a timestamped backup when overwriting an existing
// file, then writes data to a temporary file in the same directory, applies perm,
// and atomically renames it into place.
func atomicWriteWithBackup(path string, data []byte, perm os.FileMode, now time.Time) (backupPath string, err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create parent directory %s: %w", dir, err)
	}

	if fi, statErr := os.Stat(path); statErr == nil && !fi.IsDir() {
		oldData, readErr := os.ReadFile(path)
		if readErr != nil {
			return "", fmt.Errorf("read existing file %s for backup: %w", path, readErr)
		}
		backupPath = backupPathFor(path, now)
		bakPerm := fi.Mode().Perm()
		if bakPerm == 0 {
			bakPerm = perm
		}
		if writeErr := os.WriteFile(backupPath, oldData, bakPerm); writeErr != nil {
			return "", fmt.Errorf("write backup %s: %w", backupPath, writeErr)
		}
	}

	tmp, err := os.CreateTemp(dir, ".ofam-setup-*.tmp")
	if err != nil {
		return backupPath, fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return backupPath, fmt.Errorf("chmod temp file %s: %w", tmpPath, err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return backupPath, fmt.Errorf("write temp file %s: %w", tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return backupPath, fmt.Errorf("sync temp file %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return backupPath, fmt.Errorf("close temp file %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return backupPath, fmt.Errorf("rename %s to %s: %w", tmpPath, path, err)
	}
	cleanup = false
	return backupPath, nil
}
