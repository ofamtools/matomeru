package setup

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SyncStampFilename stores the SHA-256 hash of the last successfully synced
// embedded setup assets inside Matomeru's state directory.
const SyncStampFilename = "setup-assets.sha256"

// AssetsHash returns a deterministic SHA-256 hex digest over all embedded setup
// assets (OFAM rules, embedded skills, and the OMP extension).
func AssetsHash() string {
	h := sha256.New()
	writePart := func(name string, data []byte) {
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(data)
		h.Write([]byte{0})
	}

	writePart("ofam-rules.md", []byte(embeddedOFAMRules))

	var skillPaths []string
	_ = fs.WalkDir(embeddedSkillsFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		skillPaths = append(skillPaths, path)
		return nil
	})
	sort.Strings(skillPaths)
	for _, p := range skillPaths {
		raw, err := fs.ReadFile(embeddedSkillsFS, p)
		if err != nil {
			continue
		}
		writePart(p, raw)
	}

	writePart("omp-extension", []byte(embeddedOMPExtension))
	return hex.EncodeToString(h.Sum(nil))
}

// SyncIfChanged runs RunMachine when the embedded assets hash differs from the
// recorded stamp in stateDir (or when force is true). When already up to date,
// it returns an empty OK Report and ran=false without touching any target files.
func SyncIfChanged(opts Options, stateDir string, force bool) (Report, bool, error) {
	wantHash := AssetsHash()
	stampPath := ""
	if strings.TrimSpace(stateDir) != "" {
		stampPath = filepath.Join(stateDir, SyncStampFilename)
	}

	if !force && stampPath != "" {
		if raw, err := os.ReadFile(stampPath); err == nil && strings.TrimSpace(string(raw)) == wantHash {
			return Report{
				OK:      true,
				DryRun:  opts.DryRun,
				Check:   opts.Check,
				Actions: []Action{},
			}, false, nil
		}
	}

	rep, err := RunMachine(opts)
	if err != nil {
		return rep, true, err
	}

	if rep.OK && !opts.DryRun && !opts.Check && stampPath != "" {
		if werr := writeFileAtomic(stampPath, []byte(wantHash+"\n"), 0o644); werr != nil {
			return rep, true, fmt.Errorf("write setup sync stamp %s: %w", stampPath, werr)
		}
	}
	return rep, true, nil
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".ofam-stamp-*.tmp")
	if err != nil {
		return err
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
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

// RemoveManagedBlock cleanly strips ONLY the <!-- ofam:begin --> ... <!-- ofam:end -->
// managed block from existing, preserving surrounding user prose with zero
// fragments or double blank-line seams. Malformed (orphan, reversed, or
// duplicate) markers return an error without modifying existing.
func RemoveManagedBlock(existing string) (cleaned string, removed bool, err error) {
	beginCount := strings.Count(existing, OFAMBeginMarker)
	endCount := strings.Count(existing, OFAMEndMarker)
	if beginCount > 1 || endCount > 1 {
		return existing, false, errors.New("malformed OFAM managed block: duplicate markers found")
	}
	if beginCount != endCount {
		return existing, false, errors.New("malformed OFAM managed block: orphan marker found")
	}
	if beginCount == 0 {
		return existing, false, nil
	}

	beginIdx := strings.Index(existing, OFAMBeginMarker)
	endIdx := strings.Index(existing, OFAMEndMarker)
	if endIdx < beginIdx {
		return existing, false, errors.New("malformed OFAM managed block: end marker appears before begin marker")
	}

	before := existing[:beginIdx]
	after := existing[endIdx+len(OFAMEndMarker):]
	hadEndNL := false
	if strings.HasPrefix(after, "\r\n") {
		after = after[2:]
		hadEndNL = true
	} else if strings.HasPrefix(after, "\n") {
		after = after[1:]
		hadEndNL = true
	}

	beforeEmpty := strings.TrimSpace(before) == ""
	afterEmpty := strings.TrimSpace(after) == ""

	switch {
	case beforeEmpty && afterEmpty:
		return "", true, nil
	case beforeEmpty:
		return strings.TrimLeft(after, "\r\n"), true, nil
	case afterEmpty:
		return strings.TrimRight(before, "\r\n") + "\n", true, nil
	default:
		bTrim := strings.TrimRight(before, "\r\n")
		aTrim := strings.TrimLeft(after, "\r\n")
		beforeNL := strings.Count(before[len(bTrim):], "\n")
		afterNL := strings.Count(after[:len(after)-len(aTrim)], "\n")
		sep := ""
		switch {
		case beforeNL >= 2 || afterNL >= 1:
			sep = "\n\n"
		case beforeNL == 1 || hadEndNL:
			sep = "\n"
		}
		return bTrim + sep + aTrim, true, nil
	}
}

// PurgeAgents removes ONLY the <!-- ofam:begin --> ... <!-- ofam:end --> managed
// block from each global agent instruction file, preserving surrounding user
// prose and deduplicating symlinked targets.
func PurgeAgents(opts Options) ([]Action, error) {
	home, err := resolveHome(opts.Home)
	if err != nil {
		return nil, err
	}

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

		act := Action{
			Component: "agents",
			Target:    tgt.Name,
			Path:      resolved,
		}

		fi, statErr := os.Stat(resolved)
		if os.IsNotExist(statErr) {
			act.Status = StatusUnchanged
			act.Detail = "file does not exist"
			actions = append(actions, act)
			continue
		}
		if statErr != nil {
			return actions, fmt.Errorf("stat %s: %w", resolved, statErr)
		}
		if fi.IsDir() {
			return actions, fmt.Errorf("agent target %s (%s) is a directory", tgt.Name, resolved)
		}

		raw, readErr := os.ReadFile(resolved)
		if readErr != nil {
			return actions, fmt.Errorf("read %s: %w", resolved, readErr)
		}
		mode := os.FileMode(0o644)
		if p := fi.Mode().Perm(); p != 0 {
			mode = p
		}

		cleaned, removed, err := RemoveManagedBlock(string(raw))
		if err != nil {
			return actions, fmt.Errorf("%s (%s): %w", tgt.Name, resolved, err)
		}

		switch {
		case !removed:
			act.Status = StatusUnchanged
			act.Detail = "no OFAM managed block"
		case opts.DryRun:
			act.Status = StatusWouldPurge
		default:
			backup, werr := atomicWriteWithBackup(resolved, []byte(cleaned), mode, opts.now())
			if werr != nil {
				return actions, fmt.Errorf("write %s: %w", resolved, werr)
			}
			act.Status = StatusPurged
			act.Backup = backup
		}
		actions = append(actions, act)
	}

	return actions, nil
}

// RunPurgeAgents removes only the managed OFAM policy block from global agent
// instruction files and returns a Report.
func RunPurgeAgents(opts Options) (Report, error) {
	actions, err := PurgeAgents(opts)
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
