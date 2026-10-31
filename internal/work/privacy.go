package work

import "slices"

// Privacy (the tasks plan, Owner decision 6): a project's tasks leave mtm
// (the tasks export) only when the project is marked family. A project
// with no setting is private: fail closed, and never a block, since a
// private project is simply not exported (mtm offers "mark as family" in
// one quiet Inbox item). A task's Private flag keeps it and every task
// under it private inside a family project.

// Project privacy values.
const (
	PrivacyPrivate = "private"
	PrivacyFamily  = "family"
)

// Privacies are the project setting's values.
var Privacies = []string{PrivacyPrivate, PrivacyFamily}

// ValidPrivacy reports whether v is a privacy a project can be set to.
func ValidPrivacy(v string) bool { return slices.Contains(Privacies, v) }

// PrivacyOf is the project's privacy as it reads: private unless it is
// marked family.
func PrivacyOf(p Project) string {
	if p.Settings.Privacy == PrivacyFamily {
		return PrivacyFamily
	}
	return PrivacyPrivate
}

// TaskPrivate reports whether the task is private, by its own flag or a
// task above it (the flag subtasks inherit).
func TaskPrivate(byID map[string]Task, id string) bool {
	seen := map[string]bool{}
	for cur := id; cur != "" && !seen[cur]; {
		seen[cur] = true
		t, ok := byID[cur]
		if !ok {
			return false
		}
		if t.Private {
			return true
		}
		cur = t.ParentID
	}
	return false
}

// Exports reports, for every task, whether it may leave mtm: its project
// is family and neither it nor any task above it is private. Unassociated
// tasks have no family project and stay in mtm.
func Exports(tasks []Task, projects []Project) map[string]bool {
	family := map[string]bool{}
	for _, p := range projects {
		family[p.ID] = PrivacyOf(p) == PrivacyFamily
	}
	byID := make(map[string]Task, len(tasks))
	for _, t := range tasks {
		byID[t.ID] = t
	}
	out := make(map[string]bool, len(tasks))
	for _, t := range tasks {
		out[t.ID] = t.ProjectID != "" && family[t.ProjectID] && !TaskPrivate(byID, t.ID)
	}
	return out
}
