"""Tiny client for the daemon wire protocol, and daemon start/stop helpers.

Used by compare.py. A daemon runs with HOME/XDG_* under its own temp root;
nothing reaches the owner's tuios. Stop kills only the PID in the pid file.
"""
import json, os, signal, socket, subprocess, time, uuid

def env_for(root, extra=None):
    e = {"PATH": "/usr/bin:/bin", "TERM": "xterm-256color", "SHELL": "/bin/sh",
         "HOME": f"{root}/home", "XDG_RUNTIME_DIR": f"{root}/run", "XDG_CONFIG_HOME": f"{root}/config",
         "XDG_STATE_HOME": f"{root}/state", "XDG_DATA_HOME": f"{root}/data", "XDG_CACHE_HOME": f"{root}/cache"}
    for d in ("home", "run", "config", "state", "data", "cache"):
        os.makedirs(f"{root}/{d}", mode=0o700, exist_ok=True)
    e.update(extra or {})
    return e

def sock_path(root, server):
    return f"{root}/run/tuios/servers/{server}/tuios.sock"

def call(sock, verb, params=None, timeout=10):
    s = socket.socket(socket.AF_UNIX); s.settimeout(timeout); s.connect(sock)
    rid = uuid.uuid4().hex
    s.sendall((json.dumps({"id": rid, "verb": verb, "params": params or {}}) + "\n").encode())
    buf = b""
    while not buf.endswith(b"\n"):
        c = s.recv(1 << 20)
        if not c: break
        buf += c
    s.close()
    r = json.loads(buf)
    if r.get("error"): raise RuntimeError(f"{verb}: {r['error']}")
    return r.get("result")

def start(argv, root, server, wait=30):
    sock = sock_path(root, server)
    subprocess.Popen(argv, env=env_for(root), stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
                     stderr=open(f"{root}/start.err", "w"), start_new_session=True)
    t = time.time()
    while time.time() - t < wait:
        if os.path.exists(sock) and os.path.exists(sock + ".pid"):
            try:
                call(sock, "list-sessions"); return sock
            except Exception: pass
        time.sleep(0.2)
    raise RuntimeError("daemon did not start: " + open(f"{root}/start.err").read())

def stop(sock):
    try:
        pid = int(open(sock + ".pid").read().strip())
    except Exception: return
    try: os.kill(pid, signal.SIGTERM)
    except ProcessLookupError: return
    for _ in range(100):
        try: os.kill(pid, 0)
        except ProcessLookupError: return
        time.sleep(0.05)
    os.kill(pid, signal.SIGKILL)
