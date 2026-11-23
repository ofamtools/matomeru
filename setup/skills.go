package setup

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type skillTarget struct {
	name string
	rel  string
}

var defaultSkillTargets = []skillTarget{
	{name: "antigravity", rel: filepath.Join(".gemini", "skills")},
	{name: "claude", rel: filepath.Join(".claude", "skills")},
	{name: "codex", rel: filepath.Join(".codex", "skills")},
}

type embeddedSkill struct {
	name      string
	embedPath string
}

var embeddedSkills = []embeddedSkill{
	{name: "ofam-wave-release", embedPath: "assets/skills/ofam-wave-release/SKILL.md"},
}

var ofamSkillPrefixes = []string{
	"ofam-",
	"mtm-",
	"matomeru-",
	"ochazuke-",
	"oden-",
	"o-lane",
	"o-df",
	"o-cmds",
	"o-questions",
	"o-md",
	"o-git",
	"o-json",
	"o-ai",
}

// IsOFAMSkill reports whether name belongs to the OFAM / Matomeru skill family.
func IsOFAMSkill(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for _, es := range embeddedSkills {
		if name == es.name {
			return true
		}
	}
	for _, prefix := range ofamSkillPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// SyncSkills seeds embedded skills into <home>/.agents/skills and symlinks valid
// skill directories from the canonical hub (filtered to OFAM/Matomeru skills
// unless opts.AllSkills is true) and any explicit opts.SkillsSrc directories
// into each harness's skills directory.
func SyncSkills(opts Options) ([]Action, error) {
	home, err := resolveHome(opts.Home)
	if err != nil {
		return nil, err
	}
	hubDir := filepath.Join(home, ".agents", "skills")
	actions := []Action{}

	// 1. Seed embedded skills into the canonical hub idempotently.
	seedActions, err := seedEmbeddedSkills(hubDir, opts)
	actions = append(actions, seedActions...)
	if err != nil {
		return actions, err
	}

	// 2. Discover valid skills across canonical hub and extra source directories.
	skills := map[string]string{}
	// Ensure embedded skills are visible during --dry-run / --check even before creation.
	for _, es := range embeddedSkills {
		skills[es.name] = filepath.Join(hubDir, es.name)
	}
	type skillSource struct {
		dir        string
		filterOFAM bool
	}
	sources := make([]skillSource, 0, 1+len(opts.SkillsSrc))
	sources = append(sources, skillSource{dir: hubDir, filterOFAM: !opts.AllSkills})
	for _, s := range opts.SkillsSrc {
		if trimmed := strings.TrimSpace(s); trimmed != "" {
			dir := filepath.Clean(trimmed)
			if abs, aerr := filepath.Abs(trimmed); aerr == nil {
				dir = abs
			}
			sources = append(sources, skillSource{dir: dir, filterOFAM: false})
		}
	}
	for _, src := range sources {
		entries, rerr := os.ReadDir(src.dir)
		if rerr != nil {
			if os.IsNotExist(rerr) {
				continue
			}
			return actions, fmt.Errorf("read skills source %s: %w", src.dir, rerr)
		}
		for _, ent := range entries {
			name := ent.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			if src.filterOFAM && !IsOFAMSkill(name) {
				continue
			}
			if _, exists := skills[name]; exists {
				continue
			}
			cand := filepath.Join(src.dir, name)
			if isValidSkillDir(cand) {
				skills[name] = cand
			}
		}
	}

	skillNames := make([]string, 0, len(skills))
	for name := range skills {
		skillNames = append(skillNames, name)
	}
	slices.Sort(skillNames)

	// 3. Sync skills into each harness directory, skipping shared dirs.
	for _, tgt := range defaultSkillTargets {
		targetDir := filepath.Join(home, tgt.rel)
		if isSharedSkillsDir(hubDir, targetDir) {
			actions = append(actions, Action{
				Component: "skills",
				Target:    tgt.name,
				Path:      targetDir,
				Status:    StatusUnchanged,
				Detail:    "shared dir",
			})
			continue
		}

		for _, name := range skillNames {
			srcDir := skills[name]
			linkPath := filepath.Join(targetDir, name)
			act, serr := syncOneSkill(tgt.name+"/"+name, srcDir, linkPath, opts)
			actions = append(actions, act)
			if serr != nil {
				return actions, serr
			}
		}
	}

	return actions, nil
}

func seedEmbeddedSkills(hubDir string, opts Options) ([]Action, error) {
	actions := make([]Action, 0, len(embeddedSkills))
	for _, es := range embeddedSkills {
		want, err := fs.ReadFile(embeddedSkillsFS, es.embedPath)
		if err != nil {
			return actions, fmt.Errorf("read embedded skill %s: %w", es.name, err)
		}
		skillDir := filepath.Join(hubDir, es.name)
		skillFile := filepath.Join(skillDir, "SKILL.md")
		act := Action{
			Component: "skills",
			Target:    "hub/" + es.name,
			Path:      skillFile,
		}

		if fi, lerr := os.Lstat(skillDir); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
			if !opts.Force {
				act.Path = skillDir
				act.Status = StatusSkipped
				act.Detail = "symlinked skill dir preserved (use --force to replace)"
				actions = append(actions, act)
				continue
			}
			switch {
			case opts.Check:
				act.Path = skillDir
				act.Status = StatusOutdated
				act.Detail = "symlinked skill dir would be replaced with --force"
			case opts.DryRun:
				act.Path = skillDir
				act.Status = StatusWouldUpdate
				act.Detail = "would replace symlinked skill dir with embedded copy"
			default:
				if err := os.Remove(skillDir); err != nil {
					return actions, fmt.Errorf("remove symlinked skill dir %s: %w", skillDir, err)
				}
				if _, err := atomicWriteWithBackup(skillFile, want, 0o644, opts.now()); err != nil {
					return actions, fmt.Errorf("write embedded skill %s: %w", skillFile, err)
				}
				act.Status = StatusUpdated
			}
			actions = append(actions, act)
			continue
		}

		existing, rerr := os.ReadFile(skillFile)
		exists := rerr == nil
		if rerr != nil && !os.IsNotExist(rerr) {
			return actions, fmt.Errorf("read %s: %w", skillFile, rerr)
		}

		switch {
		case exists && bytes.Equal(existing, want):
			act.Status = StatusUnchanged
		case opts.Check:
			if !exists {
				act.Status = StatusMissing
				act.Detail = "embedded skill not seeded"
			} else {
				act.Status = StatusOutdated
				act.Detail = "embedded skill differs from hub copy"
			}
		case opts.DryRun:
			if !exists {
				act.Status = StatusWouldCreate
			} else {
				act.Status = StatusWouldUpdate
			}
		default:
			backup, werr := atomicWriteWithBackup(skillFile, want, 0o644, opts.now())
			if werr != nil {
				return actions, fmt.Errorf("write %s: %w", skillFile, werr)
			}
			if !exists {
				act.Status = StatusCreated
			} else {
				act.Status = StatusUpdated
				act.Backup = backup
			}
		}
		actions = append(actions, act)
	}
	return actions, nil
}

func isValidSkillDir(dir string) bool {
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return false
	}
	sf, err := os.Stat(filepath.Join(dir, "SKILL.md"))
	return err == nil && !sf.IsDir()
}

func isSharedSkillsDir(hubDir, targetDir string) bool {
	if hubInfo, herr := os.Stat(hubDir); herr == nil {
		if tgtInfo, terr := os.Stat(targetDir); terr == nil && os.SameFile(hubInfo, tgtInfo) {
			return true
		}
	}
	// Also recognize a symlink to hubDir before hubDir has been created (e.g. --dry-run).
	if fi, err := os.Lstat(targetDir); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return sameFileOrTarget(targetDir, hubDir, nil)
	}
	return false
}

func syncOneSkill(targetName, srcDir, linkPath string, opts Options) (Action, error) {
	act := Action{
		Component: "skills",
		Target:    targetName,
		Path:      linkPath,
	}

	srcInfo, _ := os.Stat(srcDir)
	fi, lerr := os.Lstat(linkPath)
	switch {
	case os.IsNotExist(lerr):
		switch {
		case opts.Check:
			act.Status = StatusMissing
			act.Detail = fmt.Sprintf("missing symlink to %s", srcDir)
		case opts.DryRun:
			act.Status = StatusWouldLink
			act.Detail = srcDir
		default:
			if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
				return act, fmt.Errorf("mkdir %s: %w", filepath.Dir(linkPath), err)
			}
			if err := os.Symlink(srcDir, linkPath); err != nil {
				return act, fmt.Errorf("symlink %s -> %s: %w", linkPath, srcDir, err)
			}
			act.Status = StatusLinked
			act.Detail = srcDir
		}
	case lerr != nil:
		return act, fmt.Errorf("lstat %s: %w", linkPath, lerr)
	case fi.Mode()&os.ModeSymlink != 0:
		if sameFileOrTarget(linkPath, srcDir, srcInfo) {
			act.Status = StatusUnchanged
			break
		}
		curDest, _ := os.Readlink(linkPath)
		switch {
		case !opts.Force || opts.Check:
			act.Status = StatusConflict
			act.Detail = fmt.Sprintf("symlink points to %s (want %s; use --force)", curDest, srcDir)
		case opts.DryRun:
			act.Status = StatusWouldLink
			act.Detail = fmt.Sprintf("would relink %s -> %s", curDest, srcDir)
		default:
			if err := os.Remove(linkPath); err != nil {
				return act, fmt.Errorf("remove symlink %s: %w", linkPath, err)
			}
			if err := os.Symlink(srcDir, linkPath); err != nil {
				return act, fmt.Errorf("symlink %s -> %s: %w", linkPath, srcDir, err)
			}
			act.Status = StatusLinked
			act.Detail = srcDir
		}
	default:
		// Never clobber real non-symlink directories (or files) without --force.
		switch {
		case !opts.Force:
			act.Status = StatusSkipped
			act.Detail = "real directory preserved (use --force to back up and link)"
		case opts.Check:
			act.Status = StatusConflict
			act.Detail = "real directory exists instead of symlink"
		case opts.DryRun:
			act.Status = StatusWouldLink
			act.Detail = fmt.Sprintf("would back up real directory and link to %s", srcDir)
		default:
			backup := backupPathFor(linkPath, opts.now())
			if err := os.Rename(linkPath, backup); err != nil {
				return act, fmt.Errorf("backup %s to %s: %w", linkPath, backup, err)
			}
			if err := os.Symlink(srcDir, linkPath); err != nil {
				return act, fmt.Errorf("symlink %s -> %s: %w", linkPath, srcDir, err)
			}
			act.Status = StatusLinked
			act.Backup = backup
			act.Detail = srcDir
		}
	}
	return act, nil
}
