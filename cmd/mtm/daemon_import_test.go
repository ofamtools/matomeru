//go:build !nodaemon

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"ofam.dev/matomeru/internal/daemon"
)

// importTuios is the tuios binary that writes the state to import: the
// owner's running tuios is v0.10.0, so the test builds that tag
// (internal/daemon/contract/build-tuios.sh) and points MTM_IMPORT_TUIOS at it.
func importTuios(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("MTM_IMPORT_TUIOS")
	if bin == "" {
		t.Skip("MTM_IMPORT_TUIOS is unset: build tuios v0.10.0 with internal/daemon/contract/build-tuios.sh and set it to the binary")
	}
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("MTM_IMPORT_TUIOS: %v", err)
	}
	return bin
}

// treeHash is a digest of every path, mode and byte under dir ("" when it is
// absent), to show a refused import changed nothing.
func treeHash(t *testing.T, dir string) string {
	t.Helper()
	h := sha256.New()
	var lines []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		info, err := d.Info()
		if err != nil {
			return err
		}
		line := rel + "|" + info.Mode().String()
		if info.Mode().IsRegular() {
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(b)
			line += "|" + hex.EncodeToString(sum[:])
		}
		lines = append(lines, line)
		return nil
	})
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	h.Write([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(h.Sum(nil))
}

// serial: starts a tuios v0.10.0 daemon and a self daemon and sets process environment.
func TestImportStateRoundTripFromTuios(t *testing.T) {
	bin := importTuios(t)
	root, err := os.MkdirTemp("/tmp", "mtmimp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	for _, d := range []string{"home", "run", "config", "state", "data", "cache"} {
		os.MkdirAll(filepath.Join(root, d), 0o700)
	}
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "TUIOS_") || strings.HasPrefix(k, "TMUX") {
			t.Setenv(k, "")
			os.Unsetenv(k)
		}
	}
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(root, "run"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("MTM_DAEMON_RUNNER", runnerSelf)
	t.Setenv("MTM_SERVER", "")
	os.Unsetenv("MTM_SERVER")

	tuios := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env = os.Environ()
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("tuios %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	called := func(verb, params string) map[string]any {
		t.Helper()
		var v map[string]any
		out := tuios("call", verb, params)
		if err := json.Unmarshal([]byte(strings.TrimSpace(strings.SplitN(out, "\n", 2)[0])), &v); err != nil {
			t.Fatalf("%s: %v\n%s", verb, err, out)
		}
		return v
	}

	// The throwaway source: the default tuios server, two sessions.
	tuios("start-server")
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			exec.Command(bin, "kill-server").Run()
		}
	})
	called("new-session", `{"name":"alpha","window_name":"w1","cwd":"/tmp"}`)
	called("new-window", `{"session":"alpha","name":"w2","workspace":2}`)
	called("new-session", `{"name":"beta","window_name":"b1"}`)
	type pane struct {
		id, name       string
		workspace, pid int
	}
	oldPanes := map[string][]pane{}
	oldSession := map[string]string{}
	list := called("list-sessions", `{}`)
	for _, s := range list["sessions"].([]any) {
		row := s.(map[string]any)
		oldSession[row["name"].(string)] = row["id"].(string)
	}
	for name := range oldSession {
		ws := called("list-windows", `{"session":"`+name+`"}`)
		for _, w := range ws["windows"].([]any) {
			row := w.(map[string]any)
			oldPanes[name] = append(oldPanes[name], pane{row["window_id"].(string), row["display_name"].(string), int(row["workspace"].(float64)), int(row["pid"].(float64))})
		}
	}
	if len(oldSession) != 2 || len(oldPanes["alpha"]) != 2 || len(oldPanes["beta"]) != 1 {
		t.Fatalf("source: sessions %v panes %v", oldSession, oldPanes)
	}
	srcDir := filepath.Join(root, "state", "tuios", "sessions")
	waitFor(t, 15*time.Second, "the source saved its sessions", func() bool {
		for name, ps := range oldPanes {
			b, err := os.ReadFile(filepath.Join(srcDir, name+".json"))
			if err != nil {
				return false
			}
			for _, p := range ps {
				if !bytes.Contains(b, []byte(p.id)) {
					return false
				}
			}
		}
		return true
	})

	data := filepath.Join(root, "mtm.db")
	mtm := func(args ...string) (string, string, error) {
		var out, errOut bytes.Buffer
		err := run(append([]string{"--data", data, "--server", "tgt"}, args...), strings.NewReader(""), &out, &errOut)
		return out.String(), errOut.String(), err
	}
	tgtDir := filepath.Join(root, "state", "tuios-servers", "tgt", "tuios", "sessions")
	tgtRoot := filepath.Join(root, "state", "tuios-servers", "tgt")
	sourceLive := true // a running daemon keeps writing its own state, so its tree is not compared
	targetLive := false
	// What an import writes: the saved sessions, their history, the archive.
	// A running target daemon writes its own files beside them (its log), so
	// while one runs only these are compared.
	written := func() string {
		var parts []string
		for _, f := range []string{"alpha.json", "beta.json"} {
			b, _ := os.ReadFile(filepath.Join(tgtDir, f))
			parts = append(parts, f+":"+string(b))
		}
		return strings.Join(parts, "\n") + treeHash(t, filepath.Join(tgtDir, "scrollback")) + treeHash(t, filepath.Join(tgtDir, "archive"))
	}
	refuse := func(why string, args ...string) {
		t.Helper()
		before, beforeSrc, beforeWritten := treeHash(t, tgtRoot), treeHash(t, filepath.Join(root, "state", "tuios")), written()
		_, errOut, err := mtm(append([]string{"daemon", "import-state"}, args...)...)
		if err == nil {
			t.Fatalf("import %v succeeded, want a refusal containing %q", args, why)
		}
		if msg := err.Error() + errOut; !strings.Contains(msg, why) {
			t.Fatalf("import %v: %q does not contain %q", args, msg, why)
		}
		if after := treeHash(t, tgtRoot); (!targetLive && after != before) || written() != beforeWritten {
			t.Fatalf("a refused import (%s) changed the target", why)
		}
		if after := treeHash(t, filepath.Join(root, "state", "tuios")); !sourceLive && after != beforeSrc {
			t.Fatalf("a refused import (%s) changed the source", why)
		}
	}

	// 1. A live writer: the source daemon still runs.
	refuse("has a daemon running", "--from", "default")
	if _, err := os.Stat(tgtRoot); err == nil {
		t.Fatalf("the refusal created %s", tgtRoot)
	}
	tuios("kill-server")
	stopped = true
	sourceLive = false
	waitFor(t, 10*time.Second, "the source daemon stopped", func() bool { live, _ := daemon.ServerLive(daemon.SocketFor("")); return !live })
	srcHash := treeHash(t, filepath.Join(root, "state", "tuios"))

	// 2. State of a version this daemon cannot read, and unreadable state:
	// copies of the source under other names.
	for name, mutate := range map[string]func([]byte) []byte{
		"future": func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"resurrection_version": 1`), []byte(`"resurrection_version": 9`), 1)
		},
		"broken": func([]byte) []byte { return []byte("{not json") },
	} {
		dst := filepath.Join(root, "state", "tuios-servers", name, "tuios", "sessions")
		copyTree(t, srcDir, dst)
		b, err := os.ReadFile(filepath.Join(dst, "alpha.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, "alpha.json"), mutate(b), 0o600); err != nil {
			t.Fatal(err)
		}
		why := "cannot read"
		refuse(why, "--from", name)
	}
	if _, err := os.Stat(tgtRoot); err == nil {
		t.Fatalf("a refusal created %s", tgtRoot)
	}

	// 3. The dry run lists the plan and writes nothing.
	out, _, err := mtm("daemon", "import-state", "--from", "default", "--dry-run")
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	var plan daemon.ImportReport
	if err := json.Unmarshal([]byte(out[strings.Index(out, "{"):]), &plan); err != nil {
		t.Fatalf("dry run output %q: %v", out, err)
	}
	if !plan.DryRun || plan.Copied || len(plan.Sessions) != 2 || len(plan.Files) < 5 {
		t.Fatalf("dry run = %+v", plan)
	}
	if _, err := os.Stat(tgtRoot); err == nil {
		t.Fatalf("the dry run created %s", tgtRoot)
	}

	// 4. mtm's association for a pane, then the target daemon running: the
	// import refuses. (pane set starts the self daemon, empty.)
	paneID := oldPanes["alpha"][1].id
	if _, errOut, err := mtm("project", "new", "demo"); err != nil {
		t.Fatalf("project new: %v %s", err, errOut)
	}
	if _, errOut, err := mtm("pane", "set", paneID, "@owner=demo"); err != nil {
		t.Fatalf("pane set: %v %s", err, errOut)
	}
	status := func() daemonInfo {
		t.Helper()
		out, errOut, err := mtm("daemon", "status")
		var info daemonInfo
		if err != nil || json.Unmarshal([]byte(out[strings.Index(out, "{"):]), &info) != nil {
			t.Fatalf("status: %v %s %s", err, out, errOut)
		}
		return info
	}
	info := status()
	if !info.Running || info.Runner != runnerSelf {
		t.Fatalf("the target daemon: %+v", info)
	}
	t.Cleanup(func() {
		if pid := status().PID; pid > 1 {
			syscall.Kill(pid, syscall.SIGTERM)
		}
	})
	targetLive = true
	refuse("has a daemon running", "--from", "default")
	refuse("has a daemon running", "--from", "default", "--overwrite")
	if _, errOut, err := mtm("daemon", "stop", "--yes"); err != nil {
		t.Fatalf("stop: %v %s", err, errOut)
	}

	targetLive = false
	// 5. The import. A second one refuses (same names) and changes nothing.
	out, errOut, err := mtm("daemon", "import-state", "--from", "default")
	if err != nil {
		t.Fatalf("import: %v %s", err, errOut)
	}
	var done daemon.ImportReport
	if err := json.Unmarshal([]byte(out[strings.Index(out, "{"):]), &done); err != nil {
		t.Fatalf("import output %q: %v", out, err)
	}
	if !done.Copied || done.CopiedFile < 5 || len(done.Sessions) != 2 {
		t.Fatalf("import = %+v", done)
	}
	if got := treeHash(t, filepath.Join(root, "state", "tuios")); got != srcHash {
		t.Fatalf("the import changed the source (copy, never move)")
	}
	for _, f := range []string{"alpha.json", "beta.json"} {
		a, _ := os.ReadFile(filepath.Join(srcDir, f))
		b, err := os.ReadFile(filepath.Join(tgtDir, f))
		if err != nil || !bytes.Equal(a, b) {
			t.Fatalf("%s was not copied byte for byte (%v)", f, err)
		}
	}
	refuse("already holds saved state", "--from", "default")

	// 6. The self daemon restores them: the same session ids, window ids,
	// names and layout, with new processes. mtm's association still holds.
	if _, errOut, err := mtm("daemon", "start"); err != nil {
		t.Fatalf("start: %v %s", err, errOut)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := daemon.ForServer("tgt")
	sessions, err := c.Sessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("restored sessions = %+v", sessions)
	}
	for _, s := range sessions {
		if s.ID != oldSession[s.Name] {
			t.Errorf("session %s id = %s, want %s", s.Name, s.ID, oldSession[s.Name])
		}
		panes, err := c.Panes(ctx, s.Name)
		if err != nil {
			t.Fatal(err)
		}
		if len(panes) != len(oldPanes[s.Name]) {
			t.Fatalf("session %s: %d panes, want %d", s.Name, len(panes), len(oldPanes[s.Name]))
		}
		for i, p := range panes {
			old := oldPanes[s.Name][i]
			if p.ID != old.id || p.Name != old.name || p.Workspace != old.workspace {
				t.Errorf("session %s pane %d = %s %q ws %d, want %s %q ws %d", s.Name, i, p.ID, p.Name, p.Workspace, old.id, old.name, old.workspace)
			}
			if p.PID <= 0 || p.PID == old.pid {
				t.Errorf("pane %s pid = %d (was %d): the process should be new", p.ID, p.PID, old.pid)
			}
		}
	}
	got, errOut, err := mtm("pane", "get", paneID)
	if err != nil {
		t.Fatalf("pane get: %v %s", err, errOut)
	}
	var row map[string]any
	if err := json.Unmarshal([]byte(got[strings.Index(got, "{"):]), &row); err != nil {
		t.Fatal(err)
	}
	if row["project"] == "" || row["project"] == nil || row["session"] != "alpha" {
		t.Fatalf("pane %s after the import = %v: its project association was lost", paneID, row)
	}
	mtm("daemon", "stop", "--yes")
}

func waitFor(t *testing.T, d time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting: %s", what)
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
}
