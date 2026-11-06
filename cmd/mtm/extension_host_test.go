package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
)

func TestExtensionHostEnvUsesActiveActorAndCommand(t *testing.T) {
	t.Parallel()
	a := open(t)
	act := ochazuke.Owner().ThroughExtension("upstream")
	restore := a.r.WithActor(act)
	defer restore()
	env := a.baseExtEnv("fake")
	get := func(key string) string {
		for _, v := range env {
			if value, ok := strings.CutPrefix(v, key+"="); ok {
				return value
			}
		}
		return ""
	}
	var argv []string
	if err := json.Unmarshal([]byte(get("OCHAZUKE_HOST")), &argv); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(argv, a.shell(nil, nil, nil).MCPCommand) {
		t.Fatalf("host argv %v", argv)
	}
	var actor ochazuke.Actor
	if err := json.Unmarshal([]byte(get("OCHAZUKE_ACTOR")), &actor); err != nil {
		t.Fatal(err)
	}
	if !actor.SameAgent(act) {
		t.Fatalf("lost active actor: %+v", actor)
	}
	if err := a.r.Settings().Set(settingExtHostCall, false); err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(a.baseExtEnv("fake"), func(v string) bool { return strings.HasPrefix(v, "OCHAZUKE_HOST=") }) {
		t.Fatal("host-call off still advertises host")
	}
}

func TestExtensionHostCallRights(t *testing.T) {
	t.Parallel()
	a := open(t)
	for _, key := range []string{"extension.peers", "extension.peer-rows"} {
		op, ok := a.r.Operation(key)
		if !ok || op.HistoryKind != ochazuke.HistoryFetch || a.r.AgentRight(op, false).Right != ochazuke.AgentAllow {
			t.Fatalf("fetch rights %s: %+v", key, op)
		}
	}
	op, _ := a.r.Operation("extension.register")
	if a.r.AgentRight(op, false).Right != ochazuke.AgentNever {
		t.Fatal("register weakened")
	}
	value, err := a.r.Settings().Get(settingExtHostRights)
	if err != nil || !value.Protected || value.Value != "agent" {
		t.Fatalf("rights setting %+v %v", value, err)
	}
	restore := a.r.WithActor(ochazuke.Owner().ThroughExtension("fake"))
	defer restore()
	err = a.r.Settings().Set(settingExtHostRights, "caller")
	if err == nil || !strings.Contains(err.Error(), "protected") {
		t.Fatalf("agent changed protected callback rights: %v", err)
	}
}

// This exercises the actual mtm shell in a fresh process, not a fake host:
// alpha -> mtm -> beta uses the same --data, with an extension agent and Via.
func TestExtensionHostExecutable(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "host with spaces")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	build := func(out, target string) {
		t.Helper()
		cmd := exec.Command("go", "build", "-o", out, target)
		if data, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", target, err, data)
		}
	}
	host := filepath.Join(dir, "mtm")
	tool := filepath.Join(dir, "o-alpha")
	build(host, ".")
	build(tool, "./testdata/host-peer")
	data := filepath.Join(dir, "mtm.db")
	a := openAt(t, data)
	bin, _ := fakeExtension(t, peerTargetCatalog, `"doc send") printf '{"data":"%s","actor":%s,"depth":"%s","chain":%s}\n' "$MTM_DATA" "$OCHAZUKE_ACTOR" "$OCHAZUKE_HOST_DEPTH" "$OCHAZUKE_HOST_CHAIN" ;;`)
	var beta ocli.CatalogDoc
	if err := json.Unmarshal([]byte(peerTargetCatalog), &beta); err != nil {
		t.Fatal(err)
	}
	if err := a.extRegistry().write("beta", extEntry{Bin: bin, Catalog: beta}); err != nil {
		t.Fatal(err)
	}
	a.Close()
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "XDG_CONFIG_HOME=" + filepath.Join(dir, "config"), "XDG_STATE_HOME=" + filepath.Join(dir, "state"), "XDG_DATA_HOME=" + filepath.Join(dir, "data"), "OCHAZUKE_ACTOR=owner", "MTM_ACTOR=owner", "OCHAZUKE_HOST=[\"inherited-must-be-removed\"]"}
	run := func(words ...string) ([]byte, string, error) {
		cmd := exec.Command(host, append([]string{"--data", data, "--socket", filepath.Join(dir, "dead.sock")}, words...)...)
		cmd.Env = env
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		return out, stderr.String(), err
	}
	// Registration runs as the owner; its catalog subprocess must have no host.
	if _, stderr, err := run("extension", "register", "alpha", "--bin", tool); err != nil {
		t.Fatalf("register: %v %s", err, stderr)
	}
	raw, stderr, err := run("alpha.tool", "probe")
	if err != nil {
		t.Fatalf("probe: %v %s", err, stderr)
	}
	var got struct {
		Peers   []ocli.HostPeer `json:"peers"`
		Data    string          `json:"data"`
		Refusal string          `json:"refusal"`
		Host    []string        `json:"host"`
		Result  struct {
			Data  string         `json:"data"`
			Actor ochazuke.Actor `json:"actor"`
			Depth string         `json:"depth"`
			Chain []string       `json:"chain"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode: %v %s", err, raw)
	}
	if got.Data != dir || got.Result.Data != dir || !slices.Contains(got.Host, data) {
		t.Fatalf("data escaped: %s", raw)
	}
	if got.Result.Actor.Name != "alpha" || got.Result.Actor.Harness != "extension" || got.Result.Actor.Via == nil || got.Result.Actor.Via.Kind != "owner" {
		t.Fatalf("actor lost provenance: %s", raw)
	}
	if got.Result.Depth != "1" || !slices.Equal(got.Result.Chain, []string{"alpha"}) {
		t.Fatalf("depth lost: %s", raw)
	}
	if !strings.Contains(got.Refusal, "never") || !slices.ContainsFunc(got.Peers, func(p ocli.HostPeer) bool { return p.Name == "beta" }) {
		t.Fatalf("peers/rights: %s", raw)
	}
	baseEnv := slices.Clone(env)
	env = append(env, "OCHAZUKE_HOST_DEPTH=3", `OCHAZUKE_HOST_CHAIN=["root","peer","root"]`)
	raw, stderr, err = run("alpha.tool", "probe")
	if err != nil {
		t.Fatalf("nested probe: %v %s", err, stderr)
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Result.Depth != "4" || !slices.Equal(got.Result.Chain, []string{"root", "peer", "root", "alpha"}) {
		t.Fatalf("nested context reset: %s", raw)
	}
	env = append(slices.Clone(baseEnv), "OCHAZUKE_HOST_DEPTH=4", `OCHAZUKE_HOST_CHAIN=["a","b","a","b"]`)
	if _, stderr, err = run("alpha.tool", "probe"); err == nil || !strings.Contains(stderr, "host call depth 5 > 4") {
		t.Fatalf("depth did not refuse: %v %s", err, stderr)
	}
	env = baseEnv
	if _, stderr, err = run("extension", "available"); err != nil {
		t.Fatalf("discovery inherited host: %v %s", err, stderr)
	}
	// Off must also scrub the inherited host, not merely omit our assignment.
	if _, stderr, err := run("setting", "set", settingExtHostCall, "false", "--scope", "file", "--yes"); err != nil {
		t.Fatalf("off: %v %s", err, stderr)
	}
	raw, stderr, err = run("alpha.tool", "probe")
	if err != nil || !bytes.Contains(raw, []byte(`"standalone": true`)) {
		t.Fatalf("off probe: %v %s %s", err, stderr, raw)
	}
}
