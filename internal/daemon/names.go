package daemon

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Layout is one table of the names a daemon gives its socket, state and
// configuration folders: the daemon's paths, in data. The mtm row is what
// the daemon linked into mtm uses (mtmd serve.Options.Names); the tuios row is
// upstream tuios's, which a tuios on PATH and an mtm daemon from before the
// rename use. Nothing branches on a row's name.
type Layout struct {
	Name        string
	Description string
	// SocketApp is the socket folder under the runtime folder
	// ($XDG_RUNTIME_DIR, or /run/user/UID when it is the user's own).
	SocketApp string
	// SocketTmp is the socket folder in /tmp without a runtime folder; the
	// user id is appended.
	SocketTmp string
	// SocketFile is the daemon socket's file name.
	SocketFile string
	// StateApp is the default server's state folder under the state home.
	StateApp string
	// StateServers holds one state home per named server, under the state
	// home ('/'-separated).
	StateServers string
	// ServerApp is a named server's state folder under its own state home;
	// "" is that state home itself.
	ServerApp string
	// ConfigApp is the daemon's configuration folder under the config home.
	ConfigApp string
}

// Layouts are the rows, the preferred first.
var Layouts = []Layout{
	{Name: "mtm", Description: "mtm's own names: socket <runtime>/mtm/servers/NAME/mtm.sock (or /tmp/mtm-UID/...), state <state home>/mtm/servers/NAME, config <config home>/mtm",
		SocketApp: "mtm", SocketTmp: "mtm-", SocketFile: "mtm.sock", StateApp: "mtm", StateServers: "mtm/servers", ServerApp: "", ConfigApp: "mtm"},
	{Name: "tuios", Description: "tuios's names, as before the rename: socket <runtime>/tuios/servers/NAME/tuios.sock (or /tmp/tuios-UID/...), state <state home>/tuios-servers/NAME/tuios, config <config home>/tuios",
		SocketApp: "tuios", SocketTmp: "tuios-", SocketFile: "tuios.sock", StateApp: "tuios", StateServers: "tuios-servers", ServerApp: "tuios", ConfigApp: "tuios"},
}

// EnvPaths names the layout for one process and everything it starts: mtm's
// daemon.paths setting is handed to the daemon this way, and the daemon's
// panes inherit it, so an mtm run in a pane prefers its daemon's layout.
const EnvPaths = "MTM_DAEMON_PATHS"

// LayoutNamed is the row of that name.
func LayoutNamed(name string) (Layout, error) {
	for _, l := range Layouts {
		if l.Name == name {
			return l, nil
		}
	}
	return Layout{}, fmt.Errorf("unknown daemon paths %q (%s)", name, strings.Join(LayoutNames(), ", "))
}

// LayoutNames lists the rows' names, the preferred first.
func LayoutNames() []string {
	out := make([]string, 0, len(Layouts))
	for _, l := range Layouts {
		out = append(out, l.Name)
	}
	return out
}

// PreferredLayout is the layout EnvPaths names, else the first row.
func PreferredLayout() Layout {
	if l, err := LayoutNamed(os.Getenv(EnvPaths)); err == nil {
		return l
	}
	return Layouts[0]
}

// SocketBase is the default server's socket folder under this layout: the
// runtime folder's SocketApp folder, or /tmp/<SocketTmp><uid>.
func (l Layout) SocketBase() string {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" && runtime.GOOS != "darwin" {
		dir := filepath.Join("/run/user", strconv.Itoa(os.Getuid()))
		if info, err := os.Lstat(dir); err == nil && info.IsDir() && info.Mode().Perm() == 0o700 {
			if stat, ok := info.Sys().(*syscall.Stat_t); ok && int(stat.Uid) == os.Getuid() {
				base = dir
			}
		}
	}
	if base == "" {
		return filepath.Join("/tmp", l.SocketTmp+strconv.Itoa(os.Getuid()))
	}
	return filepath.Join(base, l.SocketApp)
}

// Socket is the socket of the server named name under this layout, where
// the daemon puts it: <SocketBase>/servers/NAME/<SocketFile>. An empty name
// is the default server's; a name that is not one path element is "".
func (l Layout) Socket(name string) string {
	base := l.SocketBase()
	if name != "" {
		if strings.ContainsAny(name, "/\\") || name == "." || name == ".." {
			return ""
		}
		base = filepath.Join(base, "servers", name)
	}
	return filepath.Join(base, l.SocketFile)
}

// StateDir is the folder a server keeps its files in under the state home
// base: StateApp for the default server ("" or DefaultServerName), the
// ServerApp folder of <StateServers>/NAME for a named one.
func (l Layout) StateDir(base, server string) string {
	if normalServer(server) == "" {
		return join(base, l.StateApp)
	}
	return join(filepath.Join(join(base, l.StateServers), server), l.ServerApp)
}

// SessionsDir is where a server keeps its saved sessions.
func (l Layout) SessionsDir(base, server string) string {
	return filepath.Join(l.StateDir(base, server), "sessions")
}

// LogPath is the server's daemon log.
func (l Layout) LogPath(base, server string) string {
	return filepath.Join(l.StateDir(base, server), "daemon.log")
}

func join(base, rel string) string {
	if rel == "" {
		return base
	}
	return filepath.Join(base, filepath.FromSlash(rel))
}

// ResolveSocket is the socket a client of the server named name uses: the
// preferred layout's when its socket file exists, else the first other
// layout's whose daemon answers, else the preferred one's (where a daemon
// started now would listen). So a client keeps reaching a daemon started
// under the other names until that daemon is restarted.
func ResolveSocket(preferred Layout, name string) string {
	sock := preferred.Socket(name)
	if sock == "" {
		return ""
	}
	if _, err := os.Lstat(sock); err == nil {
		return sock
	}
	for _, l := range Layouts {
		if l.Name == preferred.Name {
			continue
		}
		if other := l.Socket(name); other != "" && answers(other) {
			return other
		}
	}
	return sock
}

// answers reports whether something accepts a connection on socket.
func answers(socket string) bool {
	if _, err := os.Lstat(socket); err != nil {
		return false
	}
	conn, err := net.DialTimeout("unix", socket, 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// EnvName is one pane variable under both names: the daemon exports Name
// (mtm's) beside Upstream (TUIOS_) for one release, and readers try Name
// first.
type EnvName struct {
	Name     string
	Upstream string
}

// The pane variables mtm reads, and the daemon exports under both names.
var (
	EnvMarker     = EnvName{"MTM_ENV", "TUIOS_ENV"}
	EnvSession    = EnvName{"MTM_SESSION", "TUIOS_SESSION"}
	EnvSessionID  = EnvName{"MTM_SESSION_ID", "TUIOS_SESSION_ID"}
	EnvPaneID     = EnvName{"MTM_PANE_ID", "TUIOS_PANE_ID"}
	EnvWindowID   = EnvName{"MTM_WINDOW_ID", "TUIOS_WINDOW_ID"}
	EnvWindowName = EnvName{"MTM_WINDOW_NAME", "TUIOS_WINDOW_NAME"}
	EnvSocket     = EnvName{"MTM_SOCKET", "TUIOS_SOCKET"}
	EnvServer     = EnvName{"MTM_SERVER", "TUIOS_SERVER"}
	EnvHost       = EnvName{"MTM_HOST", "TUIOS_HOST"}
	EnvPaneToken  = EnvName{"MTM_PANE_TOKEN", "TUIOS_PANE_TOKEN"}
	EnvPaneGrants = EnvName{"MTM_PANE_GRANTS", "TUIOS_PANE_GRANTS"}
	EnvPaneTTY    = EnvName{"MTM_PANE_TTY", "TUIOS_PANE_TTY"}
	EnvRestored   = EnvName{"MTM_RESTORED", "TUIOS_RESTORED"}
	EnvAgent      = EnvName{"MTM_AGENT", "TUIOS_AGENT"}
)

// EnvNames is every pair, the table the daemon exports from.
var EnvNames = []EnvName{
	EnvMarker, EnvSession, EnvSessionID, EnvPaneID, EnvWindowID, EnvWindowName, EnvSocket,
	EnvServer, EnvHost, EnvPaneToken, EnvPaneGrants, EnvPaneTTY, EnvRestored, EnvAgent,
}

// Get is the variable's value: Name's when set, else Upstream's.
func (e EnvName) Get(getenv func(string) string) string {
	if v := getenv(e.Name); v != "" {
		return v
	}
	return getenv(e.Upstream)
}

// Lookup is Get with whether either name is set.
func (e EnvName) Lookup(lookup func(string) (string, bool)) (string, bool) {
	if v, ok := lookup(e.Name); ok && v != "" {
		return v, true
	}
	return lookup(e.Upstream)
}
