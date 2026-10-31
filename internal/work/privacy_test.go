package work

import "testing"

// Exports over values only (privacy.go, no store): family projects
// export, a private flag keeps the subtree in, no setting is private.
func TestExportsOverValues(t *testing.T) {
	projects := []Project{{ID: "fam", Settings: ProjectSettings{Privacy: PrivacyFamily}}, {ID: "new"}}
	tasks := []Task{
		{ID: "a", ProjectID: "fam"},
		{ID: "s", ProjectID: "fam", Private: true},
		{ID: "c", ProjectID: "fam", ParentID: "s"},
		{ID: "g", ProjectID: "fam", ParentID: "c"},
		{ID: "n", ProjectID: "new"},
		{ID: "u"},
	}
	got := Exports(tasks, projects)
	want := map[string]bool{"a": true, "s": false, "c": false, "g": false, "n": false, "u": false}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: %v, want %v", id, got[id], w)
		}
	}
	if PrivacyOf(projects[1]) != PrivacyPrivate || !ValidPrivacy(PrivacyFamily) || ValidPrivacy("") {
		t.Fatal("privacy values")
	}
}
