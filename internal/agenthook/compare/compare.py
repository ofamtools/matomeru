#!/usr/bin/env python3
"""Compare `tuios agent-hook` (v0.12.1) with `mtm agent hook` on the same payloads.

Each daemon (tuios v0.12.1; mtm's self daemon) runs in its own temp root. Each
fixture payload (tuios internal/integration/testdata/hooks/*.json, plus
fixtures/extra/*.json) is fed to the hook of its own daemon, in a fresh window
(mode "entry") and in sequence on one window per harness file (mode "chain").
What is compared: the hook's stdout and exit code, then the pane's
get-agent-state and agent-activity (ids and times removed).

usage: compare.py TUIOS_BIN MTM_BIN FIXTURE_DIR [--send-cwd]   (FIXTURE_DIR: ../testdata/hooks)
"""
import glob, json, os, subprocess, sys
import wire

TUIOS, MTM, FIX = sys.argv[1:4]
SEND_CWD = "--send-cwd" in sys.argv
BASE = "/tmp/mtmd-4-agent/cmp-run"
DROP = {"agent_state_at", "evidence_age_ms", "at", "window_id", "window", "id"}

def clean(v):
    if isinstance(v, dict): return {k: clean(x) for k, x in v.items() if k not in DROP}
    if isinstance(v, list): return [clean(x) for x in v]
    return v

class D:
    def __init__(self, name, argv, hookargv):
        self.name, self.hookargv = name, hookargv
        self.root = f"{BASE}/{name}"
        subprocess.run(["rm", "-rf", self.root])
        os.makedirs(self.root)
        self.sock = wire.start(argv, self.root, "cmp")
        wire.call(self.sock, "new-session", {"name": "s1", "width": 100, "height": 30})
    def window(self, label):
        return wire.call(self.sock, "new-window", {"session": "s1", "name": label, "command": ["/bin/sh"], "focus": False})["window_id"]
    def hook(self, harness, e, wid):
        extra = {"TUIOS_SOCKET": self.sock, "TUIOS_SERVER": "cmp", "TUIOS_SESSION": "s1", "TUIOS_PANE_ID": wid, "TUIOS_ENV": "1"}
        extra.update(e.get("env") or {})
        env = wire.env_for(self.root, extra)
        data = json.dumps(e["payload"]) if "payload" in e else e["raw"]
        p = subprocess.run(self.hookargv + [harness] + ([e["event"]] if e.get("event") else []) + ["--integration", "1"] + (["--send-cwd"] if SEND_CWD and self.name == "mtm" else []),
                           input=data.encode(), env=env, capture_output=True, timeout=20)
        return p.returncode, p.stdout.decode()
    def observe(self, wid):
        st = wire.call(self.sock, "get-agent-state", {"session": "s1", "window": wid})
        ac = wire.call(self.sock, "agent-activity", {"session": "s1", "window": wid})
        return clean({"state": st, "activity": ac})
    def stop(self): wire.stop(self.sock)

def fixtures():
    out = {}
    for f in sorted(glob.glob(f"{FIX}/**/*.json", recursive=True)):
        out[f] = json.load(open(f))
    return out

def main():
    a = D("tuios", [TUIOS, "--server", "cmp", "start-server"], [TUIOS, "agent-hook"])
    b = D("mtm", [MTM, "__mtmd", "--server", "cmp", "start-server"], [MTM, "agent", "hook"])
    fails, total = [], 0
    try:
        for f, entries in fixtures().items():
            harness = os.path.basename(f)[:-5]
            chain = {d.name: d.window("chain-" + harness) for d in (a, b)}
            for i, e in enumerate(entries):
                for mode in ("entry", "chain"):
                    w = {d.name: (d.window(f"e{i}") if mode == "entry" else chain[d.name]) for d in (a, b)}
                    res = {}
                    for d in (a, b):
                        rc, out = d.hook(harness, e, w[d.name])
                        res[d.name] = clean({"rc": rc, "stdout": out, "obs": d.observe(w[d.name])})
                    total += 1
                    if res["tuios"] != res["mtm"]:
                        fails.append((f"{harness}/{e['name']}/{mode}", res))
    finally:
        a.stop(); b.stop()
    for name, res in fails:
        t, m = json.dumps(res["tuios"], sort_keys=True, indent=1).splitlines(), json.dumps(res["mtm"], sort_keys=True, indent=1).splitlines()
        print("DIFF", name)
        import difflib
        for l in difflib.unified_diff(t, m, "tuios", "mtm", lineterm="", n=0): print("   ", l)
    print(f"{total} comparisons, {len(fails)} differ" + (" (mtm run with --send-cwd)" if SEND_CWD else ""))
    sys.exit(1 if fails else 0)

main()
