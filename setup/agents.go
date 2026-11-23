package setup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// OFAMBeginMarker opens the managed OFAM policy block inside agent instruction files.
	OFAMBeginMarker = "<!-- ofam:begin -->"
	// OFAMEndMarker closes the managed OFAM policy block inside agent instruction files.
	OFAMEndMarker = "<!-- ofam:end -->"
)

// AgentTarget describes one harness instruction file managed relative to HOME.
type AgentTarget struct {
	Name string
	Rel  string
}

// DefaultAgentTargets lists the canonical global instruction files for each harness.
var DefaultAgentTargets = []AgentTarget{
	{Name: "antigravity", Rel: filepath.Join(".gemini", "GEMINI.md")},
	{Name: "codex", Rel: filepath.Join(".codex", "AGENTS.md")},
	{Name: "claude", Rel: filepath.Join(".claude", "CLAUDE.md")},
	{Name: "shared", Rel: filepath.Join(".agents", "AGENTS.md")},
}

// UpsertManagedBlock inserts or replaces the <!-- ofam:begin --> ... <!-- ofam:end -->
// block inside existing while preserving all surrounding prose byte-for-byte.
// Malformed (orphan, reversed, or duplicate) markers return an error without change.
func UpsertManagedBlock(existing, block string) (merged string, changed bool, err error) {
	trimmed := strings.TrimSpace(block)
	if !strings.HasPrefix(trimmed, OFAMBeginMarker) || !strings.HasSuffix(trimmed, OFAMEndMarker) {
		trimmed = OFAMBeginMarker + "\n" + trimmed + "\n" + OFAMEndMarker
	}

	beginCount := strings.Count(existing, OFAMBeginMarker)
	endCount := strings.Count(existing, OFAMEndMarker)
	if beginCount > 1 || endCount > 1 {
		return existing, false, errors.New("malformed OFAM managed block: duplicate markers found")
	}
	if beginCount != endCount {
		return existing, false, errors.New("malformed OFAM managed block: orphan marker found")
	}

	if beginCount == 1 {
		beginIdx := strings.Index(existing, OFAMBeginMarker)
		endIdx := strings.Index(existing, OFAMEndMarker)
		if endIdx < beginIdx {
			return existing, false, errors.New("malformed OFAM managed block: end marker appears before begin marker")
		}
		endAfter := endIdx + len(OFAMEndMarker)
		merged = existing[:beginIdx] + trimmed + existing[endAfter:]
		return merged, merged != existing, nil
	}

	if strings.TrimSpace(existing) == "" {
		merged = trimmed + "\n"
		return merged, merged != existing, nil
	}

	switch {
	case strings.HasSuffix(existing, "\n\n"):
		merged = existing + trimmed + "\n"
	case strings.HasSuffix(existing, "\n"):
		merged = existing + "\n" + trimmed + "\n"
	default:
		merged = existing + "\n\n" + trimmed + "\n"
	}
	return merged, merged != existing, nil
}

// ConfigureAgents updates or links all global agent instruction files.
func ConfigureAgents(opts Options) ([]Action, error) {
	home, err := resolveHome(opts.Home)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(opts.Source) != "" {
		return configureAgentsSourceMode(home, opts)
	}
	return configureAgentsBlockMode(home, opts)
}

func resolveClaudeWrapperPath(target AgentTarget, path string) string {
	if target.Name != "claude" {
		return path
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return path
	}
	if strings.TrimSpace(string(raw)) == "@AGENTS.md" {
		return filepath.Join(filepath.Dir(path), "AGENTS.md")
	}
	return path
}

// resolveWritableTarget follows any symlinks so writes always land on the real
// target file and never replace a symlink with a regular file.
func resolveWritableTarget(path string) (string, error) {
	fi, err := os.Lstat(path)
	if err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			if eval, eerr := filepath.EvalSymlinks(path); eerr == nil {
				return eval, nil
			}
			// Broken symlink: resolve destination without overwriting the link itself.
			dest, rerr := os.Readlink(path)
			if rerr != nil {
				return "", fmt.Errorf("readlink %s: %w", path, rerr)
			}
			if !filepath.IsAbs(dest) {
				dest = filepath.Join(filepath.Dir(path), dest)
			}
			dest = filepath.Clean(dest)
			if parent, perr := filepath.EvalSymlinks(filepath.Dir(dest)); perr == nil {
				return filepath.Join(parent, filepath.Base(dest)), nil
			}
			return dest, nil
		}
		if eval, eerr := filepath.EvalSymlinks(path); eerr == nil {
			return eval, nil
		}
		return filepath.Clean(path), nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	return resolveCanonicalNonExistent(path), nil
}

func resolveCanonicalNonExistent(path string) string {
	clean := filepath.Clean(path)
	var suffix []string
	cur := clean
	for {
		parent := filepath.Dir(cur)
		if eval, err := filepath.EvalSymlinks(cur); err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				eval = filepath.Join(eval, suffix[i])
			}
			return eval
		}
		if parent == cur {
			return clean
		}
		suffix = append(suffix, filepath.Base(cur))
		cur = parent
	}
}

func configureAgentsBlockMode(home string, opts Options) ([]Action, error) {
	block := OFAMRulesBlock()
	actions := make([]Action, 0, len(DefaultAgentTargets))
	seen := make(map[string]string, len(DefaultAgentTargets))

	for _, tgt := range DefaultAgentTargets {
		path := filepath.Join(home, tgt.Rel)
		path = resolveClaudeWrapperPath(tgt, path)

		resolved, err := resolveWritableTarget(path)
		if err != nil {
			return actions, fmt.Errorf("resolve agent target %s (%s): %w", tgt.Name, path, err)
		}

		if first, dup := seen[resolved]; dup {
			actions = append(actions, Action{
				Component: "agents",
				Target:    tgt.Name,
				Path:      resolved,
				Status:    StatusUnchanged,
				Detail:    fmt.Sprintf("same target as %s", first),
			})
			continue
		}
		seen[resolved] = tgt.Name

		var existing string
		exists := false
		mode := os.FileMode(0o644)
		if fi, statErr := os.Stat(resolved); statErr == nil {
			if fi.IsDir() {
				return actions, fmt.Errorf("agent target %s (%s) is a directory", tgt.Name, resolved)
			}
			raw, readErr := os.ReadFile(resolved)
			if readErr != nil {
				return actions, fmt.Errorf("read %s: %w", resolved, readErr)
			}
			existing = string(raw)
			exists = true
			if p := fi.Mode().Perm(); p != 0 {
				mode = p
			}
		} else if !os.IsNotExist(statErr) {
			return actions, fmt.Errorf("stat %s: %w", resolved, statErr)
		}

		merged, changed, err := UpsertManagedBlock(existing, block)
		if err != nil {
			return actions, fmt.Errorf("%s (%s): %w", tgt.Name, resolved, err)
		}

		act := Action{
			Component: "agents",
			Target:    tgt.Name,
			Path:      resolved,
		}
		switch {
		case !changed:
			act.Status = StatusUnchanged
		case opts.Check:
			if !exists {
				act.Status = StatusMissing
				act.Detail = "file does not exist"
			} else {
				act.Status = StatusOutdated
				act.Detail = "OFAM managed block missing or outdated"
			}
		case opts.DryRun:
			if !exists {
				act.Status = StatusWouldCreate
			} else {
				act.Status = StatusWouldUpdate
			}
		default:
			backup, werr := atomicWriteWithBackup(resolved, []byte(merged), mode, opts.now())
			if werr != nil {
				return actions, fmt.Errorf("write %s: %w", resolved, werr)
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

func configureAgentsSourceMode(home string, opts Options) ([]Action, error) {
	srcAbs, err := filepath.Abs(opts.Source)
	if err != nil {
		return nil, fmt.Errorf("resolve --source %s: %w", opts.Source, err)
	}
	srcInfo, err := os.Stat(srcAbs)
	if err != nil {
		return nil, fmt.Errorf("--source %s: %w", srcAbs, err)
	}
	if srcInfo.IsDir() {
		return nil, fmt.Errorf("--source %s is a directory", srcAbs)
	}

	actions := make([]Action, 0, len(DefaultAgentTargets))
	for _, tgt := range DefaultAgentTargets {
		path := filepath.Join(home, tgt.Rel)
		path = resolveClaudeWrapperPath(tgt, path)

		act := Action{
			Component: "agents",
			Target:    tgt.Name,
			Path:      path,
		}

		fi, lerr := os.Lstat(path)
		switch {
		case os.IsNotExist(lerr):
			switch {
			case opts.Check:
				act.Status = StatusMissing
				act.Detail = fmt.Sprintf("missing symlink to %s", srcAbs)
			case opts.DryRun:
				act.Status = StatusWouldLink
				act.Detail = fmt.Sprintf("would link to %s", srcAbs)
			default:
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					return actions, fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
				}
				if err := os.Symlink(srcAbs, path); err != nil {
					return actions, fmt.Errorf("symlink %s -> %s: %w", path, srcAbs, err)
				}
				act.Status = StatusLinked
				act.Detail = srcAbs
			}
		case lerr != nil:
			return actions, fmt.Errorf("lstat %s: %w", path, lerr)
		case fi.Mode()&os.ModeSymlink != 0:
			if sameFileOrTarget(path, srcAbs, srcInfo) {
				act.Status = StatusUnchanged
				act.Detail = srcAbs
				break
			}
			curDest, _ := os.Readlink(path)
			switch {
			case !opts.Force || opts.Check:
				act.Status = StatusConflict
				act.Detail = fmt.Sprintf("symlink points to %s (want %s; use --force)", curDest, srcAbs)
			case opts.DryRun:
				act.Status = StatusWouldLink
				act.Detail = fmt.Sprintf("would relink %s -> %s", curDest, srcAbs)
			default:
				if err := os.Remove(path); err != nil {
					return actions, fmt.Errorf("remove symlink %s: %w", path, err)
				}
				if err := os.Symlink(srcAbs, path); err != nil {
					return actions, fmt.Errorf("symlink %s -> %s: %w", path, srcAbs, err)
				}
				act.Status = StatusLinked
				act.Detail = srcAbs
			}
		default:
			if os.SameFile(fi, srcInfo) {
				act.Status = StatusUnchanged
				act.Detail = "same file as source"
				break
			}
			switch {
			case !opts.Force || opts.Check:
				act.Status = StatusConflict
				act.Detail = "regular file exists (use --force to back up and symlink)"
			case opts.DryRun:
				act.Status = StatusWouldLink
				act.Detail = fmt.Sprintf("would back up regular file and link to %s", srcAbs)
			default:
				backup := backupPathFor(path, opts.now())
				if err := os.Rename(path, backup); err != nil {
					return actions, fmt.Errorf("backup %s to %s: %w", path, backup, err)
				}
				if err := os.Symlink(srcAbs, path); err != nil {
					return actions, fmt.Errorf("symlink %s -> %s: %w", path, srcAbs, err)
				}
				act.Status = StatusLinked
				act.Backup = backup
				act.Detail = srcAbs
			}
		}
		actions = append(actions, act)
	}
	return actions, nil
}

func sameFileOrTarget(linkPath, wantTarget string, wantInfo os.FileInfo) bool {
	if wantInfo != nil {
		if st, err := os.Stat(linkPath); err == nil && os.SameFile(st, wantInfo) {
			return true
		}
	}
	dest, err := os.Readlink(linkPath)
	if err != nil {
		return false
	}
	if !filepath.IsAbs(dest) {
		dest = filepath.Join(filepath.Dir(linkPath), dest)
	}
	return resolveCanonicalNonExistent(dest) == resolveCanonicalNonExistent(wantTarget)
}
