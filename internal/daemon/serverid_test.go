package daemon

import "testing"

func TestServerIDIsStableAcrossTheLayouts(t *testing.T) {
	t.Parallel()
	same := [][]string{
		{"/tmp/tuios-501/servers/mtm-daemon/tuios.sock", "/tmp/mtm-501/servers/mtm-daemon/mtm.sock", "/run/user/501/mtm/servers/mtm-daemon/mtm.sock", "/run/user/501/tuios/servers/mtm-daemon/tuios.sock"},
		{"/tmp/tuios-501/tuios.sock", "/tmp/mtm-501/mtm.sock", "/run/user/501/mtm/mtm.sock"},
	}
	for _, group := range same {
		want := ServerID(group[0])
		if want == group[0] {
			t.Fatalf("%s not mapped", group[0])
		}
		for _, s := range group {
			if got := ServerID(s); got != want {
				t.Errorf("ServerID(%s) = %s, want %s", s, got, want)
			}
		}
	}
	if ServerID("/tmp/x/servers/a/mtm.sock") == ServerID("/tmp/mtm-501/servers/a/mtm.sock") {
		t.Error("a custom socket took a layout's identity")
	}
	for _, s := range []string{"", "/tmp/custom.sock"} {
		if ServerID(s) != s {
			t.Errorf("ServerID(%q) = %q", s, ServerID(s))
		}
	}
}
