package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func session(name string, version int, windows ...string) string {
	var ws []string
	for _, w := range windows {
		ws = append(ws, `{"id":"`+w+`","title":"t"}`)
	}
	v := ""
	if version >= 0 {
		v = `,"resurrection_version":` + string(rune('0'+version))
	}
	return `{"name":"` + name + `","session_id":"sid-` + name + `","windows":[` + strings.Join(ws, ",") + `]` + v + `}`
}

func importFixture(t *testing.T) (base string, o ImportOptions) {
	t.Helper()
	base = t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	src := ServerStateDir(base, "")
	write(t, filepath.Join(src, "alpha.json"), session("alpha", 1, "w1", "w2"))
	write(t, filepath.Join(src, "empty.json"), session("empty", 1))
	write(t, filepath.Join(src, "scrollback", "alpha", "w1.hist.gz"), "h1")
	write(t, filepath.Join(src, "scrollback", "gone", "w9.hist.gz"), "orphan")
	write(t, filepath.Join(src, "attention", "items.json"), "{}")
	write(t, filepath.Join(src, "grants", "applied.json"), "{}")
	write(t, filepath.Join(src, "push", "vapid.pem"), "secret")
	write(t, filepath.Join(src, "stale.json.tmp"), "x")
	return base, ImportOptions{From: "default", To: "tgt", Base: base, Live: func(string) (bool, string) { return false, "" }}
}

func actions(r ImportReport) map[string]string {
	m := map[string]string{}
	for _, f := range r.Files {
		m[f.Path] = f.Action
	}
	return m
}

func TestImportPlanAndCopy(t *testing.T) {
	base, o := importFixture(t)
	o.DryRun = true
	rep, err := ImportState(o)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"alpha.json": "copy", "empty.json": "skip", "scrollback/alpha/w1.hist.gz": "copy", "scrollback/gone/w9.hist.gz": "skip",
		"attention/items.json": "copy", "grants/applied.json": "skip", "push/vapid.pem": "skip", "stale.json.tmp": "skip",
	}
	got := actions(rep)
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %q, want %q", k, got[k], v)
		}
	}
	if len(rep.Sessions) != 1 || rep.Sessions[0].ID != "sid-alpha" || len(rep.Sessions[0].Windows) != 2 {
		t.Fatalf("sessions = %+v", rep.Sessions)
	}
	if _, err := os.Stat(filepath.Join(base, "tuios-servers")); err == nil {
		t.Fatal("a dry run created the target")
	}
	o.DryRun = false
	rep, err = ImportState(o)
	if err != nil || !rep.Copied || rep.CopiedFile != 3 {
		t.Fatalf("import: %+v, %v", rep, err)
	}
	dst := ServerStateDir(base, "tgt")
	for _, f := range []string{"alpha.json", "scrollback/alpha/w1.hist.gz", "attention/items.json"} {
		if _, err := os.Stat(filepath.Join(dst, f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	for _, f := range []string{"empty.json", "scrollback/gone", "grants", "push", "stale.json.tmp"} {
		if _, err := os.Stat(filepath.Join(dst, f)); err == nil {
			t.Errorf("%s was copied", f)
		}
	}
	if _, err := os.Stat(filepath.Join(ServerStateDir(base, ""), "alpha.json")); err != nil {
		t.Error("the source lost a file: the import must copy")
	}
}

func TestImportRefusals(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(base string, o *ImportOptions)
		want   error
	}{
		{"source live", func(_ string, o *ImportOptions) {
			o.Live = func(s string) (bool, string) { return s == SocketFor(""), "test" }
		}, ErrSourceLive},
		{"target live", func(_ string, o *ImportOptions) {
			o.Live = func(s string) (bool, string) { return s == SocketFor("tgt"), "test" }
		}, ErrTargetLive},
		{"newer version", func(base string, _ *ImportOptions) {
			write(t, filepath.Join(ServerStateDir(base, ""), "alpha.json"), session("alpha", 9, "w1"))
		}, ErrUnreadable},
		{"corrupt", func(base string, _ *ImportOptions) {
			write(t, filepath.Join(ServerStateDir(base, ""), "alpha.json"), "{")
		}, ErrUnreadable},
		{"same server", func(_ string, o *ImportOptions) { o.To = "default" }, ErrSameServer},
		{"conflict", func(base string, _ *ImportOptions) {
			write(t, filepath.Join(ServerStateDir(base, "tgt"), "alpha.json"), session("alpha", 1, "x"))
		}, ErrConflict},
		{"no source", func(_ string, o *ImportOptions) { o.From = "nosuch" }, ErrNothing},
		{"bad name", func(_ string, o *ImportOptions) { o.From = "../x" }, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base, o := importFixture(t)
			tc.mutate(base, &o)
			before := listTree(t, filepath.Join(base, "tuios-servers"))
			_, err := ImportState(o)
			if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if after := listTree(t, filepath.Join(base, "tuios-servers")); after != before {
				t.Fatalf("a refusal changed the target:\n%s\n---\n%s", before, after)
			}
		})
	}
}

func listTree(t *testing.T, dir string) string {
	t.Helper()
	var out []string
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := os.ReadFile(p)
			out = append(out, p+"="+string(b))
		}
		return nil
	})
	return strings.Join(out, "\n")
}

func TestImportOverwriteArchivesAndRollsBack(t *testing.T) {
	base, o := importFixture(t)
	dst := ServerStateDir(base, "tgt")
	write(t, filepath.Join(dst, "alpha.json"), "OLD")
	o.Overwrite = true
	o.Now = func() time.Time { return time.Date(2026, 11, 8, 1, 2, 3, 0, time.UTC) }
	rep, err := ImportState(o)
	if err != nil {
		t.Fatal(err)
	}
	held, err := os.ReadFile(filepath.Join(dst, "archive", "import-20261108T010203Z", "alpha.json"))
	if err != nil || string(held) != "OLD" || rep.Archived == "" {
		t.Fatalf("archived = %q, %v", held, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "alpha.json")); !strings.Contains(string(b), "sid-alpha") {
		t.Fatalf("alpha.json not replaced: %s", b)
	}

	// A source that changes while it is copied: everything is undone.
	base, o = importFixture(t)
	dst = ServerStateDir(base, "tgt")
	write(t, filepath.Join(dst, "attention", "items.json"), "KEEP")
	calls := 0
	o.Live = func(s string) (bool, string) {
		// The plan checks both servers under every layout; the next check is
		// ImportState's own, just before copying: touch the source then, as
		// a writer that started late would.
		if calls++; calls == 2*len(Layouts)+1 {
			write(t, filepath.Join(ServerStateDir(base, ""), "alpha.json"), session("alpha", 1, "w1", "w2", "w3"))
		}
		return false, ""
	}
	if _, err := ImportState(o); !errors.Is(err, ErrSourceLive) {
		t.Fatalf("err = %v, want a live-writer refusal", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "alpha.json")); err == nil {
		t.Error("alpha.json left behind")
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "attention", "items.json")); string(b) != "KEEP" {
		t.Errorf("attention/items.json = %q, want the original restored", b)
	}
}

// A source saved under tuios's names (a tuios, or an mtm daemon from before
// the rename) is found and imported into the target under mtm's.
func TestImportFindsASourceUnderTheOtherLayout(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv(EnvPaths, "")
	tuios, _ := LayoutNamed("tuios")
	src := tuios.SessionsDir(base, "old")
	write(t, filepath.Join(src, "alpha.json"), session("alpha", 1, "w1"))
	rep, err := ImportState(ImportOptions{From: "old", To: "tgt", Base: base, Live: func(string) (bool, string) { return false, "" }})
	if err != nil {
		t.Fatal(err)
	}
	if rep.SourceDir != src || rep.TargetDir != filepath.Join(base, "mtm", "servers", "tgt", "sessions") {
		t.Fatalf("source %s target %s", rep.SourceDir, rep.TargetDir)
	}
	if _, err := os.Stat(filepath.Join(rep.TargetDir, "alpha.json")); err != nil {
		t.Fatal(err)
	}
	// A daemon of the source under either names refuses the import.
	live := tuios.Socket("old")
	_, err = ImportState(ImportOptions{From: "old", To: "tgt2", Base: base, Live: func(s string) (bool, string) { return s == live, "socket" }})
	if !errors.Is(err, ErrSourceLive) {
		t.Fatalf("err = %v, want the source's tuios-named daemon to refuse", err)
	}
}
