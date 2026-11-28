package data

import (
	"slices"
	"strings"
	"time"
)

// Pure scope mode values controlling cross-project filtering and defaults.
const (
	ScopeModeGlobal  = "global"
	ScopeModeToday   = "today"
	ScopeModeProject = "project"
)

// ScopeModes lists all valid scope modes in cycle order.
var ScopeModes = []string{ScopeModeGlobal, ScopeModeToday, ScopeModeProject}

// IsValidScopeMode reports whether raw names a known scope mode.
func IsValidScopeMode(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case ScopeModeGlobal, ScopeModeToday, ScopeModeProject:
		return true
	}
	return false
}

// NormalizeScopeMode canonicalizes raw to a valid ScopeMode, defaulting to ScopeModeGlobal.
func NormalizeScopeMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case ScopeModeToday:
		return ScopeModeToday
	case ScopeModeProject:
		return ScopeModeProject
	default:
		return ScopeModeGlobal
	}
}

// SetPinnedProject updates the pinned project in cfg. Clearing the pinned
// project while in project mode resets the scope mode to global.
func SetPinnedProject(cfg FocusConfig, projectID string) FocusConfig {
	cfg.PinnedProjectID = strings.TrimSpace(projectID)
	if cfg.PinnedProjectID == "" && NormalizeScopeMode(cfg.ScopeMode) == ScopeModeProject {
		cfg.ScopeMode = ScopeModeGlobal
	}
	return cfg
}

// SetScopeMode updates the persisted scope mode in cfg.
func SetScopeMode(cfg FocusConfig, mode string) FocusConfig {
	cfg.ScopeMode = NormalizeScopeMode(mode)
	return cfg
}

// NextScopeMode returns the next scope mode in cycle order (global -> today -> project),
// skipping project when hasPinned is false.
func NextScopeMode(current string, hasPinned bool) string {
	switch NormalizeScopeMode(current) {
	case ScopeModeGlobal:
		return ScopeModeToday
	case ScopeModeToday:
		if hasPinned {
			return ScopeModeProject
		}
		return ScopeModeGlobal
	default:
		return ScopeModeGlobal
	}
}

// Kind predicates for pure domain checks without identity conditionals in UI layers.
func IsTaskKind(kind string) bool    { return kind == KindTask }
func IsNoteKind(kind string) bool    { return kind == KindNote }
func IsCaptureKind(kind string) bool { return kind == KindCapture }

// SchemaHasProjectScope reports whether schema carries a project or task reference field.
func SchemaHasProjectScope(schema Schema) bool {
	if IsTaskKind(schema.Kind) {
		return true
	}
	for _, name := range []string{"project", "task"} {
		if f, ok := schema.Field(name); ok && slices.Contains(f.RefKinds, KindTask) {
			return true
		}
	}
	return false
}

// IsTodayTime reports whether t falls on the same local calendar day as now.
func IsTodayTime(t, now time.Time) bool {
	if t.IsZero() || now.IsZero() {
		return false
	}
	tl, nl := t.Local(), now.Local()
	return tl.Year() == nl.Year() && tl.YearDay() == nl.YearDay()
}

// IsRecordActiveToday reports whether rec was created/updated today or is in an
// active/in-progress work or agent state.
func IsRecordActiveToday(rec Record, now time.Time) bool {
	if IsTodayTime(rec.UpdatedAt, now) || IsTodayTime(rec.CreatedAt, now) {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(rec.Text("workStatus")), "active") {
		return true
	}
	st := strings.ToLower(strings.TrimSpace(rec.Text("agentStatus")))
	return st == AgentStatusWorking || st == AgentStatusNeedsYou
}

// FilterTreeNodesByProject keeps only the top-level project tree matching projectID.
func FilterTreeNodesByProject(nodes []TreeNode, projectID string) []TreeNode {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nodes
	}
	for _, nd := range nodes {
		if nd.Record.ID == projectID {
			return []TreeNode{nd}
		}
	}
	return nil
}

// FilterTreeNodesToday keeps subtrees containing records active or updated today
// (or the capture bucket).
func FilterTreeNodesToday(nodes []TreeNode, cfg FocusConfig, now time.Time) []TreeNode {
	out := make([]TreeNode, 0, len(nodes))
	for _, nd := range nodes {
		if cfg.CaptureBucketID != "" && nd.Record.ID == cfg.CaptureBucketID {
			out = append(out, nd)
			continue
		}
		kids := FilterTreeNodesToday(nd.Children, cfg, now)
		if IsRecordActiveToday(nd.Record, now) || len(kids) > 0 {
			cp := nd
			cp.Children = kids
			out = append(out, cp)
		}
	}
	return out
}

// FilterCapturesByScope filters capture records according to mode, pinnedProjectID,
// and today's priority projects.
func FilterCapturesByScope(records []Record, mode, pinnedProjectID string, priorityProjectIDs []string, now time.Time) []Record {
	switch NormalizeScopeMode(mode) {
	case ScopeModeProject:
		if strings.TrimSpace(pinnedProjectID) == "" {
			return records
		}
		out := make([]Record, 0, len(records))
		for _, rec := range records {
			if projRef, ok := rec.RefField("project"); ok && projRef.ID == pinnedProjectID {
				out = append(out, rec)
			}
		}
		return out
	case ScopeModeToday:
		out := make([]Record, 0, len(records))
		for _, rec := range records {
			if isCaptureInTodayScope(rec, priorityProjectIDs, now) {
				out = append(out, rec)
			}
		}
		return out
	default:
		return records
	}
}

func isCaptureInTodayScope(rec Record, priorityProjectIDs []string, now time.Time) bool {
	if IsTodayTime(captureSortTime(rec), now) {
		return true
	}
	if len(priorityProjectIDs) == 0 {
		return false
	}
	projRef, ok := rec.RefField("project")
	return ok && slices.Contains(priorityProjectIDs, projRef.ID)
}
