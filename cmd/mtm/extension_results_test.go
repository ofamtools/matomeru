package main

import (
	"strings"
	"testing"

	"ofam.dev/ochazuke"
)

// The extension answers through a real process, including a remote-only
// guard. Reuses the earlier text-results lane's fixture and late-answer cases.
func TestExtensionResultPresentation(t *testing.T) {
	t.Parallel()
	const catalog = `{"schema":1,"name":"o-fake","version":"1","nouns":[{"name":"item","verbs":[{"name":"list","kind":"application"},{"name":"read","kind":"application","result":"text"}]}],"bindings":[{"noun":"item","mode":"normal","keys":"g v","when":"remote-only","request":{"noun":"item","verb":"read"}}],"pages":[]}`
	for _, change := range []string{"none", "focus", "page", "off", "palette"} {
		t.Run(change, func(t *testing.T) {
			bin, _ := fakeExtension(t, catalog, `"item read") printf '%s\n' '"Decision\nUse staged rollout."' ;;`)
			a := registered(t, seeded(t), "fake", bin)
			openExtPage(t, a, "ext/fake")
			if err := a.r.SetFocus("ext/fake/item/i1"); err != nil {
				t.Fatal(err)
			}
			if change == "palette" {
				pressJob(t, a, ":")
				typeText(t, a, "fake.item read")
				pressJob(t, a, "enter")
			} else {
				pressJob(t, a, "g")
				_, cmd := a.Update(keyMsg("v"))
				switch change {
				case "focus":
					a.r.SetFocus("ext/fake/item/i2")
				case "page":
					a.r.OpenPage("work")
				case "off":
					a.r.Settings().Set(ochazuke.KeyTextResultsSetting, false)
				}
				settleJobs(t, a, cmd)
			}
			if change == "none" || change == "palette" {
				in := a.r.Input(a.r.Focus)
				if a.r.TopModal() != ochazuke.TextViewID || in == nil || string(in.Text) != "Decision\nUse staged rollout." {
					t.Fatalf("viewer missing: %s", frame(a))
				}
				if strings.Contains(frame(a), `item read: "Decision`) {
					t.Fatal("text flattened under viewer")
				}
				pressJob(t, a, "esc")
				if a.r.Focus != "ext/fake/item/i1" {
					t.Fatal(a.r.Focus)
				}
			} else if a.r.TopModal() != "" {
				t.Fatal("late result covered new interaction")
			}
		})
	}
}
