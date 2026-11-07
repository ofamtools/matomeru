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
	for _, change := range []string{"none", "focus", "page", "off", "palette", "all-structured", "all-string"} {
		t.Run(change, func(t *testing.T) {
			spec, answer := catalog, `"item read") printf '%s\n' '"Decision\nUse staged rollout."' ;;`
			if strings.HasPrefix(change, "all-") {
				spec = strings.Replace(catalog, `"result":"text"`, `"result":"JSON value"`, 1)
				answer = `"item read") printf '%s\n' '{"decision":"approved"}' ;;`
				if change == "all-string" {
					answer = `"item read") printf '%s\n' '"ordinary answer"' ;;`
				}
			}
			bin, _ := fakeExtension(t, spec, answer)
			a := registered(t, seeded(t), "fake", bin)
			openExtPage(t, a, "ext/fake")
			if err := a.r.SetFocus("ext/fake/item/i1"); err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(change, "all-") {
				if err := a.r.Settings().Set(ochazuke.PaletteTextResultsSetting, "all"); err != nil {
					t.Fatal(err)
				}
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
				in := a.r.Input(a.r.FocusID())
				if a.r.TopModal() != ochazuke.TextViewID || in == nil || string(in.Text) != "Decision\nUse staged rollout." {
					t.Fatalf("viewer missing: %s", frame(a))
				}
				if strings.Contains(frame(a), `item read: "Decision`) {
					t.Fatal("text flattened under viewer")
				}
				pressJob(t, a, "esc")
				if a.r.FocusID() != "ext/fake/item/i1" {
					t.Fatal(a.r.FocusID())
				}
			} else if a.r.TopModal() != "" {
				t.Fatal("late result covered new interaction")
			}
			if strings.HasPrefix(change, "all-") {
				want := `item read: {"decision":"approved"}`
				if change == "all-string" {
					want = `item read: "ordinary answer"`
				}
				if !strings.Contains(frame(a), want) {
					t.Fatalf("unpresented answer lost its status %q:\n%s", want, frame(a))
				}
			}
		})
	}
}

func TestExtensionUserRowKeepsGuard(t *testing.T) {
	t.Parallel()
	const catalog = `{"schema":1,"name":"o-fake","version":"1","nouns":[{"name":"item","verbs":[{"name":"list","kind":"application"},{"name":"read","kind":"application","result":"text"}]}],"bindings":[{"noun":"item","mode":"normal","keys":"v","when":"remote-only","request":{"noun":"item","verb":"read"}}]}`
	bin, _ := fakeExtension(t, catalog, `"item read") printf '%s\n' '"full answer"' ;;`)
	a := registered(t, seeded(t), "fake", bin)
	openExtPage(t, a, "ext/fake")
	if _, err := a.r.LoadBindings([]byte(`[{"noun":"fake.item","mode":"normal","keys":"Z","when":"input","request":{"noun":"fake.item","verb":"read"}}]`)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.r.LoadBindings([]byte(`[{"noun":"fake.item","mode":"normal","keys":"Q","when":"no-such-guard","request":{"noun":"fake.item","verb":"read"}}]`)); err == nil {
		t.Fatal("unknown user guard was erased")
	}
	pressJob(t, a, "Z")
	if a.r.TopModal() != "" {
		t.Fatal("host guard did not prevent the key")
	}
	pressJob(t, a, "v")
	if a.r.TopModal() != ochazuke.TextViewID {
		t.Fatal("catalog guard was evaluated in the host")
	}
}
