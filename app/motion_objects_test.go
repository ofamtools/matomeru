package app

import (
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/traits"
)

func setBlockMotion(a *App, policy string) {
	a.cfg.userSettings[SetBlockMotion] = policy
	a.reloadSettings()
	a.applySettings()
}

// nav.blockMotion: outside a component with its own blocks, } walks to the
// first stop of the next region (block) or five stops inside the region
// (five-stops). The old motion.block.next command still moves like }.
func TestBlockMotionPolicy(t *testing.T) {
	for _, policy := range []string{BlockMotionBlock, BlockMotionFiveStops} {
		a, d := configured(t, t.TempDir())
		setBlockMotion(a, policy)
		d.Frame()
		d.Keys("l")
		if a.Focus() != "site.toc.screen" {
			t.Fatalf("start focus %q", a.Focus())
		}
		var toc []string
		for _, n := range a.focusables(a.State()) {
			if n.region == "site.toc" {
				toc = append(toc, n.id)
			}
		}
		d.Keys("}")
		want := "site.screen"
		if policy == BlockMotionFiveStops {
			want = toc[min(5, len(toc)-1)]
		}
		if a.Focus() != want {
			t.Fatalf("%s: } focused %q, want %q (toc %v)", policy, a.Focus(), want, toc)
		}
		if policy != BlockMotionBlock {
			continue
		}
		d.Keys("{")
		if a.Focus() != "site.toc.screen" {
			t.Fatalf("{ focused %q, want the toc's first stop", a.Focus())
		}
		d.Send(core.CommandEvent{Command: core.Command{ID: traits.CmdMotionBlockNext, Actor: User}})
		if a.Focus() != "site.screen" {
			t.Fatalf("motion.block.next focused %q, want site.screen", a.Focus())
		}
	}
}

// ) in the reader goes to the next section (heading).
func TestReaderSectionMotion(t *testing.T) {
	a, d := configured(t, t.TempDir())
	body := "# One\n\n" + strings.Repeat("text\n\n", 40) + "## Two\n\n" + strings.Repeat("more\n\n", 40) + "## Three\n\nend\n"
	note, _, err := a.Store.Create(data.CreateInput{Kind: data.KindNote, Fields: map[string]any{"title": "Sections", "body": body}}, User)
	if err != nil {
		t.Fatal(err)
	}
	d.Keys("g", "n", "/")
	d.Type("notes")
	d.Keys("enter")
	selectRow(a.nav.tables[workTable], data.RefText(note.Ref()))
	a.viewMemo.gen++
	d.Keys("o")
	reader := a.activeMarkdown("reader")
	if a.Mode() != ModeReader || reader == nil {
		t.Fatalf("o did not open the reader: mode %q", a.Mode())
	}
	m := reader.Model()
	d.Keys(")")
	if want := m.Headings[1].Line; m.Offset != want {
		t.Fatalf(") scrolled to %d, want the second heading at %d", m.Offset, want)
	}
}

// i p while visual select is on selects the focused group of the rail; with
// visual off the row does not fire. In the Work tree, where i edits inline,
// i still fires at once while visual is on.
func TestObjectSelectBlock(t *testing.T) {
	a, d := configured(t, t.TempDir())
	d.Frame()
	tbl := a.nav.tables["panes.rail.table"]
	if tbl == nil || tbl.GroupField == "" {
		t.Fatalf("rail table is not grouped: %+v", tbl)
	}
	d.Keys("j", "i", "p")
	tbl = a.nav.tables["panes.rail.table"]
	if tbl.VisualActive || tbl.MotionSelection != nil {
		t.Fatalf("i p without visual selected %+v", tbl.MotionSelection)
	}
	d.Keys("v", "i", "p")
	tbl = a.nav.tables["panes.rail.table"]
	rng, ok := traits.CurrentObject(tbl.MotionNodes(), tbl.SelectedIdx, traits.ObjectBlock)
	if !ok || !tbl.VisualActive || tbl.VisualAnchor != rng.Start || tbl.SelectedIdx != rng.End {
		t.Fatalf("v i p: visual=%v anchor=%d cursor=%d, want block %+v", tbl.VisualActive, tbl.VisualAnchor, tbl.SelectedIdx, rng)
	}

	d.Keys("esc", "2")
	d.Frame()
	if !a.inWorkTree(a.State()) {
		t.Fatalf("work space is not a tree")
	}
	d.Keys("v", "i")
	if a.Mode() == ModeNormal {
		t.Fatalf("i in the Work tree with visual on did not fire at once (mode %q)", a.Mode())
	}
}
