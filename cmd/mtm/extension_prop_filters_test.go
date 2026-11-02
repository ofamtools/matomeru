package main

import (
	"testing"

	"ofam.dev/ochazuke"
)

// Catalog row data must survive mtm's project scoping, including for nouns
// with no declared props (Questions, Commands and Docs all use derived fields).
func TestExtensionPropFiltersSurviveScoping(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, props string }{{"derived", ``}, {"declared", `,"props":[{"name":"key","key":true},{"name":"name","role":"label"}]`}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			props := tc.props
			catalog := `{"schema":1,"name":"o-fake","version":"1","nouns":[{"name":"item"` + props + `,"verbs":[{"name":"rows","kind":"application","rows":"item"}]}],"pages":[{"id":"questions","title":"Questions","lists":[{"id":"questions/items","noun":"item","source":"item.rows"}]}]}`
			bin, _ := fakeExtension(t, catalog, `"item rows") echo '[{"id":"q1","label":"Open question","data":{"status":"open","project_id":"alpha"}},{"id":"q2","label":"Answered question","data":{"status":"answered","project_id":"beta"},"tones":[{"cell":0,"tone":"state.done"}]}]' ;;`)
			path := seeded(t)
			a := registered(t, path, "fake", bin)
			openExtPage(t, a, "ext/fake/questions")
			list := a.r.List("ext/fake/questions/items")
			if list == nil || list.Shown() != 2 {
				t.Fatalf("list: %v", list)
			}
			for _, scope := range []string{extScopeAuto, extScopeOff, extScopeAuto} {
				if err := a.r.Settings().Set(extScopeSetting("fake"), scope); err != nil {
					t.Fatal(err)
				}
				if err := list.SetPropFilters([]ochazuke.PropFilter{{Prop: "status", Value: "answered"}}); err != nil {
					t.Fatal(err)
				}
				if list.Shown() != 1 {
					t.Fatalf("scope %s: shown %d", scope, list.Shown())
				}
				row, ok := list.Selected()
				if !ok || row.Data != "q2" || row.Props["project_id"] != "beta" || len(row.Tones) != 1 {
					t.Fatalf("row metadata lost: %+v", row)
				}
				if props != "" && (row.Props["key"] != "q2" || row.Props["name"] != "Answered question") {
					t.Fatalf("catalog key/label defaults lost: %v", row.Props)
				}
				a.reloadExtension(a.extension("fake"))
				settleJobs(t, a, a.r.PendingCommands())
				if list.Shown() != 1 || len(list.PropFilters()) != 1 {
					t.Fatalf("reload lost filter: %d %v", list.Shown(), list.PropFilters())
				}
			}
			if err := list.SetPropFilters(nil); err != nil {
				t.Fatal(err)
			}
			if err := list.Focus(); err != nil {
				t.Fatal(err)
			}
			pressJob(t, a, "z")
			pressJob(t, a, "/")
			if a.r.RecordEditor("list-filter-form") == nil {
				t.Fatal("filter form did not open")
			}
		})
	}
}

func TestExtensionPropFiltersUnderProjectPin(t *testing.T) {
	t.Parallel()
	for _, projectField := range []string{"project", "effective_project", "project_id"} {
		t.Run(projectField, func(t *testing.T) {
			t.Parallel()
			catalog := `{"schema":1,"name":"o-fake","version":"1","nouns":[{"name":"item","verbs":[{"name":"rows","kind":"application","rows":"item"}]}],"pages":[{"id":"filter-fixture","title":"Pinned filter fixture","lists":[{"id":"filter-fixture/list","noun":"item","source":"item.rows"}]}]}`
			bin, _ := fakeExtension(t, catalog, `"item rows") echo '[{"id":"q1","label":"Open","data":{"`+projectField+`":"matomeru","status":"open"}},{"id":"q2","label":"Answered","data":{"`+projectField+`":"matomeru","status":"answered"}},{"id":"q3","label":"Other project","data":{"`+projectField+`":"ochazuke","status":"answered"}},{"id":"q4","label":"Unassociated","data":{"`+projectField+`":"","status":"answered"}}]' ;;`)
			a := registered(t, seeded(t), "fake", bin)
			pin := func(project string) {
				t.Helper()
				executeA(t, a, "project", "pin", "work", ochazuke.Arguments{"project": project})
			}
			pin("matomeru")
			openExtPage(t, a, "ext/fake/filter-fixture")
			list := a.r.List("ext/fake/filter-fixture/list")
			if list == nil || list.Shown() != 2 {
				t.Fatalf("pin must show two project rows: %v", list)
			}
			filters := []ochazuke.PropFilter{{Prop: projectField, Value: "matomeru"}, {Prop: "status", Value: "answered"}}
			if err := list.SetPropFilters(filters); err != nil {
				t.Fatal(err)
			}
			check := func(want int) {
				t.Helper()
				if list.Shown() != want {
					t.Fatalf("pin %s: shown %d, want %d", a.pin, list.Shown(), want)
				}
				if want == 1 {
					row, ok := list.Selected()
					if !ok || row.Data != "q2" || row.Props[projectField] != "matomeru" {
						t.Fatalf("matching catalog row lost: %+v", row)
					}
				}
			}
			check(1)
			pin("ochazuke")
			check(0)
			pin("matomeru")
			check(1)
			a.reloadExtension(a.extension("fake"))
			settleJobs(t, a, a.r.PendingCommands())
			check(1)
		})
	}
}
