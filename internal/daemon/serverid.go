package daemon

import (
	"path/filepath"
	"strings"
)

// ServerID is the identity records keep for the server behind socket. A
// socket path is not stable: the path rename moved it from
// /tmp/tuios-UID/servers/NAME/tuios.sock to /tmp/mtm-UID/servers/NAME/mtm.sock
// (any Layouts row), though the server and its panes stayed. A socket laid
// out by any row is "server:NAME" (the default server "server:default");
// any other path (a custom --socket) is its cleaned path; "" stays "".
// Records written with a path are mapped once by the store migration.
func ServerID(socket string) string {
	if socket == "" {
		return ""
	}
	socket = filepath.Clean(socket)
	dir, file := filepath.Split(socket)
	dir = filepath.Clean(dir)
	for _, l := range Layouts {
		if file != l.SocketFile {
			continue
		}
		name := DefaultServerName
		app := dir
		if filepath.Base(filepath.Dir(dir)) == "servers" {
			name = filepath.Base(dir)
			app = filepath.Dir(filepath.Dir(dir))
		}
		leaf := filepath.Base(app)
		if leaf == l.SocketApp || strings.HasPrefix(leaf, l.SocketTmp) && isDigits(leaf[len(l.SocketTmp):]) {
			return "server:" + name
		}
	}
	return socket
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
