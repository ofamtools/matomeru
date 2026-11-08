#!/usr/bin/env python3
"""statusline, state and absent-daemon checks: tuios v0.12.1 against mtm.
usage: compare_misc.py TUIOS_BIN MTM_BIN"""
import json, os, subprocess, sys
import wire
TUIOS, MTM = sys.argv[1:3]
BASE = "/tmp/mtmd-4-agent/cmp-misc"
DROP = {"agent_state_at", "evidence_age_ms", "at", "window_id", "window", "id"}
def clean(v):
    if isinstance(v, dict): return {k: clean(x) for k, x in v.items() if k not in DROP}
    if isinstance(v, list): return [clean(x) for x in v]
    return v
def run(argv, root, sock, wid, data=b"", extra=None):
    env = wire.env_for(root, {"TUIOS_SOCKET": sock, "TUIOS_SERVER": "cmp", "TUIOS_SESSION": "s1", "TUIOS_PANE_ID": wid, "TUIOS_ENV": "1", **(extra or {})})
    p = subprocess.run(argv, input=data, env=env, capture_output=True, timeout=20)
    return p.returncode, p.stdout.decode(), p.stderr.decode()
daemons = {}
for name, argv in (("tuios", [TUIOS, "--server", "cmp", "start-server"]), ("mtm", [MTM, "__mtmd", "--server", "cmp", "start-server"])):
    root = f"{BASE}/{name}"; subprocess.run(["rm", "-rf", root]); os.makedirs(root)
    sock = wire.start(argv, root, "cmp")
    wire.call(sock, "new-session", {"name": "s1", "width": 100, "height": 30})
    daemons[name] = (root, sock)
bad = 0
def check(label, fn):
    global bad
    res = {}
    for name, (root, sock) in daemons.items():
        wid = wire.call(sock, "new-window", {"session": "s1", "name": label, "command": ["/bin/sh"], "focus": False})["window_id"]
        out = fn(name, root, sock, wid)
        out["obs"] = clean(wire.call(sock, "get-agent-state", {"session": "s1", "window": wid}))
        res[name] = out
    ok = res["tuios"] == res["mtm"]
    bad += not ok
    print(("same  " if ok else "DIFF  ") + label)
    if not ok: print(json.dumps(res, indent=1, sort_keys=True))
try:
    sl = json.dumps({"session_id": "c1", "model": {"id": "claude-opus-5", "display_name": "Opus"}, "context_window": {"used_percentage": 42}, "cost": {"total_cost_usd": 1.5}}).encode()
    def statusline(name, root, sock, wid):
        argv = [TUIOS, "agent-statusline", "claude-code"] if name == "tuios" else [MTM, "agent", "statusline", "claude-code"]
        rc, out, err = run(argv, root, sock, wid, sl)
        return {"rc": rc, "stdout": out}
    check("statusline", statusline)
    def statusline_then(name, root, sock, wid):
        argv = [TUIOS, "agent-statusline", "claude-code", "--then", "cat >/dev/null; echo mine"] if name == "tuios" else [MTM, "agent", "statusline", "claude-code", "--then", "cat >/dev/null; echo mine"]
        rc, out, err = run(argv, root, sock, wid, sl)
        return {"rc": rc, "stdout": out}
    check("statusline --then", statusline_then)
    def state(args):
        def f(name, root, sock, wid):
            argv = ([TUIOS, "set-agent-state"] if name == "tuios" else [MTM, "agent", "state"]) + args
            rc, out, err = run(argv, root, sock, wid)
            return {"rc": rc, "stdout": out, "stderr": err}
        return f
    check("state working", state(["working"]))
    check("state needs_input -m kind", state(["needs_input", "-m", "approve Bash: make", "--kind", "approval", "--agent-session-id", "5f1c"]))
    check("state done --if-state needs_input (pane none: not applied)", state(["done", "--if-state", "needs_input"]))
    check("state bogus", state(["bogus"]))
    # absent daemon: nothing listens; the hook must exit 0, print nothing, start nothing
    root = f"{BASE}/absent"; subprocess.run(["rm", "-rf", root]); os.makedirs(root)
    wire.env_for(root)
    sock = wire.sock_path(root, "gone")
    for argv in ([MTM, "agent", "hook", "claude-code"], [MTM, "agent", "statusline", "claude-code"]):
        rc, out, err = run(argv, root, sock, "w1", b'{"hook_event_name":"Stop","session_id":"s"}', {"TUIOS_SERVER": "gone"})
        started = os.path.exists(sock) or os.path.exists(os.path.dirname(os.path.dirname(sock)))
        ok = rc == 0 and out == "" and not started
        bad += not ok
        print(("same  " if ok else "DIFF  ") + "absent daemon: " + " ".join(argv[1:3]), rc, repr(out), "socket dir made:", started)
    # state with no daemon is an error for the person who typed it
    rc, out, err = run([MTM, "agent", "state", "working"], root, sock, "w1", b"", {"TUIOS_SERVER": "gone"})
    print("state, no daemon: rc", rc, err.strip())
finally:
    for _, sock in daemons.values(): wire.stop(sock)
sys.exit(1 if bad else 0)
