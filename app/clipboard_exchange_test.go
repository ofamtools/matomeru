package app

import (
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

func clipTitles(t *testing.T, a *App) []string {
	t.Helper()
	clips, err := a.Store.ListClips(data.ClipFilter{})
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(clips))
	for i, c := range clips {
		out[i] = c.Text("authorKind") + ":" + c.Text("clipKind") + ":" + c.Text("content")
	}
	return out
}

func TestClipboardExchangePushAndCopy(t *testing.T) {
	a, d := demo(t)
	orig := activeClipboardEnv
	t.Cleanup(func() { activeClipboardEnv = orig })
	activeClipboardEnv = clipboardEnv{
		goos:     "darwin",
		lookPath: func(file string) (string, error) { return "/usr/bin/" + file, nil },
		runCmd:   func(string, ...string) ([]byte, error) { return []byte("from the system clipboard"), nil },
	}

	// An empty shared clipboard says so instead of opening an empty picker.
	d.Keys("g", "y")
	if a.Modal() != "" || !strings.Contains(a.status, "shared clipboard is empty") {
		t.Fatalf("empty open: modal %q status %q", a.Modal(), a.status)
	}

	// With nothing yanked, g Y pushes the system clipboard as the owner.
	d.Keys("g", "Y")
	if got := clipTitles(t, a); !slices.Equal(got, []string{"owner:text:from the system clipboard"}) {
		t.Fatalf("push from system clipboard = %v", got)
	}
	// A yank is pushed with its kind; pushing it twice shares it once.
	a.yank("~/proj/matomeru/app/app.go", "path")
	d.Keys("g", "Y")
	d.Keys("g", "Y")
	if got := clipTitles(t, a); len(got) != 2 || got[0] != "owner:path:~/proj/matomeru/app/app.go" {
		t.Fatalf("push from register = %v", got)
	}

	// An agent shares a clip; the picker lists it first with its badges.
	agent, err := a.Store.PutClip(data.ClipInput{Content: "go test ./data -run TestClip", Kind: data.ClipKindCode, AuthorKind: data.ClipAuthorAgent}, core.Actor{Kind: "agent", ID: "w1"})
	if err != nil {
		t.Fatal(err)
	}
	d.Keys("g", "y")
	pm := a.topPicker()
	if a.Modal() != clipboardModalID || pm == nil {
		t.Fatalf("g y opened modal %q", a.Modal())
	}
	items := pm.Model().Filtered()
	if len(items) != 3 || items[0].ID != agent.ID {
		t.Fatalf("picker items = %+v", items)
	}
	texts := func(i int) []string {
		var out []string
		for _, b := range items[i].Badges {
			out = append(out, b.Text)
		}
		return out
	}
	if !slices.Equal(texts(0), []string{"agent", "code"}) || !slices.Equal(texts(1), []string{"owner", "path"}) || !slices.Equal(texts(2), []string{"owner"}) {
		t.Fatalf("badges = %v / %v / %v", texts(0), texts(1), texts(2))
	}
	if frame := d.Frame(); !strings.Contains(frame, "Shared Clipboard") || !strings.Contains(frame, "go test ./data -run TestClip") {
		t.Fatalf("picker frame:\n%s", frame)
	}

	// enter copies the clip: registers, system clipboard and copiedAt.
	d.Keys("enter")
	const want = "go test ./data -run TestClip"
	if a.Modal() != "" || a.Register(core.RegUnnamed) != want || a.Register(core.RegClipboard) != want || a.rt.live.clipboard != want {
		t.Fatalf("copy: modal %q registers %q / %q", a.Modal(), a.Register(core.RegUnnamed), a.Register(core.RegClipboard))
	}
	if !slices.ContainsFunc(a.Shell.LastEffects, func(e core.Effect) bool {
		c, ok := e.(core.CustomEffect)
		return ok && c.Kind == effectClipboard && c.Data == want
	}) {
		t.Fatalf("the clip did not go to the system clipboard: %+v", a.Shell.LastEffects)
	}
	if got, err := a.Store.GetClip(agent.ID); err != nil || got.Text("copiedAt") == "" {
		t.Fatalf("copiedAt not stamped: %+v (%v)", got, err)
	}
}
