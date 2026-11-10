package daemon

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestLayoutPaths(t *testing.T) {
	mtm, err := LayoutNamed("mtm")
	if err != nil {
		t.Fatal(err)
	}
	tuios, err := LayoutNamed("tuios")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		got, want string
	}{
		{mtm.StateDir("/b", "srv"), "/b/mtm/servers/srv"},
		{mtm.SessionsDir("/b", "srv"), "/b/mtm/servers/srv/sessions"},
		{mtm.LogPath("/b", DefaultServerName), "/b/mtm/daemon.log"},
		{tuios.SessionsDir("/b", "srv"), "/b/tuios-servers/srv/tuios/sessions"},
		{tuios.LogPath("/b", ""), "/b/tuios/daemon.log"},
	} {
		if c.got != filepath.FromSlash(c.want) {
			t.Errorf("got %s, want %s", c.got, c.want)
		}
	}
	if _, err := LayoutNamed("nope"); err == nil {
		t.Fatal("an unknown layout resolved")
	}
}

func TestSocketForFollowsTheLayout(t *testing.T) {
	dir := runtimeDir(t)
	if got, want := SocketFor("srv"), filepath.Join(dir, "mtm", "servers", "srv", "mtm.sock"); got != want {
		t.Fatalf("SocketFor = %s, want %s", got, want)
	}
	t.Setenv(EnvPaths, "tuios")
	if got, want := SocketFor("srv"), filepath.Join(dir, "tuios", "servers", "srv", "tuios.sock"); got != want {
		t.Fatalf("SocketFor under %s=tuios = %s, want %s", EnvPaths, got, want)
	}
	t.Setenv("XDG_RUNTIME_DIR", "")
	// Without XDG_RUNTIME_DIR, Linux falls back to a private /run/user/UID
	// when the host has one (a root shell on vps2 does); else, and on macOS,
	// to /tmp/mtm-UID. The test cannot remove the host's /run/user/UID, so
	// it accepts either fallback.
	got := (Layouts[0]).SocketBase()
	tmp := filepath.Join("/tmp", "mtm-"+strconv.Itoa(os.Getuid()))
	run := filepath.Join("/run/user", strconv.Itoa(os.Getuid()), "mtm")
	if got != tmp && got != run {
		t.Fatalf("SocketBase without a runtime dir = %s, want %s or %s", got, tmp, run)
	}
}

// A daemon still listening under the other names (one started before the
// rename) is reached until it is restarted; a new socket wins once it exists.
func TestResolveSocketFallsBackToALiveDaemonUnderTheOtherNames(t *testing.T) {
	dir := runtimeDir(t)
	mtm, tuios := Layouts[0], Layouts[1]
	if got := ResolveSocket(mtm, "srv"); got != mtm.Socket("srv") {
		t.Fatalf("nothing live: %s", got)
	}
	listen(t, tuios.Socket("srv"))
	if got := ResolveSocket(mtm, "srv"); got != filepath.Join(dir, "tuios", "servers", "srv", "tuios.sock") {
		t.Fatalf("live old daemon not reached: %s", got)
	}
	listen(t, mtm.Socket("srv"))
	if got := ResolveSocket(mtm, "srv"); got != mtm.Socket("srv") {
		t.Fatalf("the preferred socket did not win: %s", got)
	}
}

func TestEnvNameReadsMtmFirst(t *testing.T) {
	env := map[string]string{"TUIOS_PANE_ID": "old"}
	get := func(k string) string { return env[k] }
	if got := EnvPaneID.Get(get); got != "old" {
		t.Fatalf("TUIOS_ fallback: %q", got)
	}
	env["MTM_PANE_ID"] = "new"
	if got := EnvPaneID.Get(get); got != "new" {
		t.Fatalf("MTM_ first: %q", got)
	}
	seen := map[string]bool{}
	for _, e := range EnvNames {
		if seen[e.Name] || seen[e.Upstream] {
			t.Fatalf("duplicate pair %v", e)
		}
		seen[e.Name], seen[e.Upstream] = true, true
	}
}
