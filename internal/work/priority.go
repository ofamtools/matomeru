package work

import "slices"

// Priority is how much a task or a project matters. None is the unset
// value: a record written before priority existed, or one set back to
// none, stores nothing and reads none.
const (
	PriorityNone   = "none"
	PriorityLow    = "low"
	PriorityMedium = "medium"
	PriorityHigh   = "high"
	PriorityUrgent = "urgent"
)

// Priorities are the values a priority can be set to, lowest first: the
// prop's enum, so a sort by priority ranks in this order.
var Priorities = []string{PriorityNone, PriorityLow, PriorityMedium, PriorityHigh, PriorityUrgent}

// ValidPriority reports whether v is a priority a record can be set to.
func ValidPriority(v string) bool { return slices.Contains(Priorities, v) }

// NormalPriority is a stored priority as it reads: "" is none; any other
// value is kept as it is.
func NormalPriority(v string) string {
	if v == "" {
		return PriorityNone
	}
	return v
}

// StoredPriority is the value to store for a priority set to v: none
// stores nothing, so the row is as it was before the field existed.
func StoredPriority(v string) string {
	if v == PriorityNone {
		return ""
	}
	return v
}
