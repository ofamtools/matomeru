// Package daemon is the TUIOS daemon client lifted from matomeru-old: one
// JSON line per call over the unix socket, event subscriptions, start-agent,
// capture, and the binary stream-pane protocol with size leases.
package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const maxLine = 32 << 20

// Client talks to one daemon socket. Server names the TUIOS server the
// socket belongs to when the client may start it (see Ensure); a client
// with no Server only connects.
type Client struct {
	Socket string
	Server string
	Start  StartFunc     // how Ensure starts Server; nil runs StartServer
	Wait   time.Duration // how long Ensure waits for a started server; 0 is ensureWait
	mu     sync.Mutex    // serializes Ensure, so two calls start one server
}

// New is the client for socket; an empty socket is TUIOS_SOCKET, else the
// socket of the server TUIOS_SERVER names (the default daemon when unset).
func New(socket string) *Client {
	if socket == "" {
		socket = os.Getenv("TUIOS_SOCKET")
	}
	if socket == "" {
		socket = SocketFor(os.Getenv("TUIOS_SERVER"))
	}
	return &Client{Socket: socket}
}

// SocketFor is the socket of the TUIOS server named name under the runtime
// dir (XDG_RUNTIME_DIR/tuios, /run/user/UID or /tmp/tuios-UID), where
// tuios --server NAME puts it: servers/NAME/tuios.sock. An empty name is
// the default daemon's socket; a name that is not one path element is "".
func SocketFor(name string) string {
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
		base = filepath.Join("/tmp", "tuios-"+strconv.Itoa(os.Getuid()))
	} else {
		base = filepath.Join(base, "tuios")
	}
	if name != "" {
		if strings.ContainsAny(name, "/\\") || name == "." || name == ".." {
			return ""
		}
		base = filepath.Join(base, "servers", name)
	}
	return filepath.Join(base, "tuios.sock")
}

// ForServer is the client of the TUIOS server named name, which Ensure
// starts when it is not running.
func ForServer(name string) *Client { return &Client{Socket: SocketFor(name), Server: name} }

type Error struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Hint    map[string]any `json:"hint,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

type response struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *Error          `json:"error"`
}

type connection struct {
	net.Conn
	reader *bufio.Reader
	stop   func() bool
	once   sync.Once
}

func (c *connection) Close() error {
	var err error
	c.once.Do(func() { c.stop(); err = c.Conn.Close() })
	return err
}
func (c *Client) dial(ctx context.Context) (*connection, error) {
	if c.Socket == "" {
		return nil, errors.New("daemon socket is unresolved; supply -socket PATH or TUIOS_SOCKET")
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", c.Socket)
	if err != nil {
		return nil, fmt.Errorf("connect daemon %s (set -socket PATH): %w", c.Socket, err)
	}
	result := &connection{Conn: conn, reader: bufio.NewReader(conn)}
	result.stop = context.AfterFunc(ctx, func() { conn.Close() })
	return result, nil
}
func readLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		part, err := r.ReadSlice('\n')
		if len(line)+len(part) > maxLine {
			return nil, errors.New("daemon JSON line exceeds 32 MiB")
		}
		line = append(line, part...)
		if err == bufio.ErrBufferFull {
			continue
		}
		return line, err
	}
}
func sendRequest(c *connection, verb string, params any) (string, error) {
	if params == nil {
		params = struct{}{}
	}
	id := newID()
	err := json.NewEncoder(c).Encode(struct {
		ID     string `json:"id"`
		Verb   string `json:"verb"`
		Params any    `json:"params"`
	}{id, verb, params})
	return id, err
}
func exchange(c *connection, verb string, params, out any) error {
	id, err := sendRequest(c, verb, params)
	if err != nil {
		return err
	}
	line, err := readLine(c.reader)
	if err != nil {
		return err
	}
	var reply response
	if err = json.Unmarshal(line, &reply); err != nil {
		return fmt.Errorf("decode %s reply: %w", verb, err)
	}
	if reply.ID != id {
		return fmt.Errorf("daemon %s reply ID mismatch", verb)
	}
	if reply.Error != nil {
		return reply.Error
	}
	if len(reply.Result) == 0 {
		return fmt.Errorf("daemon %s reply has no result", verb)
	}
	if out != nil {
		return json.Unmarshal(reply.Result, out)
	}
	return nil
}
func contextError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
func (c *Client) Call(ctx context.Context, verb string, params, out any) error {
	host, params, err := routeParams(ctx, params)
	if err != nil {
		return err
	}
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if host != "" && host != "local" {
		if err := exchange(conn, "open-host-connection", map[string]any{"host": host, "ssh_auth_sock": os.Getenv("SSH_AUTH_SOCK")}, nil); err != nil {
			return contextError(ctx, err)
		}
	}
	return contextError(ctx, exchange(conn, verb, params, out))
}

func (c *Client) Subscribe(ctx context.Context, types []string, afterSeq uint64, bootID string) (<-chan Event, <-chan error) {
	events := make(chan Event, 256)
	failures := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(failures)
		conn, err := c.dial(ctx)
		if err != nil {
			failures <- err
			return
		}
		defer conn.Close()
		params := map[string]any{"types": types}
		if afterSeq != 0 || bootID != "" {
			params["after_seq"] = afterSeq
			params["boot_id"] = bootID
		}
		id, err := sendRequest(conn, "subscribe", params)
		if err != nil {
			failures <- contextError(ctx, err)
			return
		}
		for {
			line, err := readLine(conn.reader)
			if err != nil {
				failures <- contextError(ctx, err)
				return
			}
			var raw map[string]json.RawMessage
			if err = json.Unmarshal(line, &raw); err != nil {
				failures <- err
				return
			}
			if data, ok := raw["error"]; ok {
				var failure Error
				if err = json.Unmarshal(data, &failure); err != nil {
					failures <- err
				} else {
					failures <- &failure
				}
				return
			}
			if result, ok := raw["result"]; ok {
				var replyID string
				_ = json.Unmarshal(raw["id"], &replyID)
				if replyID != id {
					failures <- errors.New("subscribe reply ID mismatch")
					return
				}
				if err = json.Unmarshal(result, &raw); err != nil {
					failures <- err
					return
				}
			}
			ev, err := decodeEvent(raw)
			if err != nil {
				failures <- err
				return
			}
			select {
			case events <- ev:
			case <-ctx.Done():
				failures <- ctx.Err()
				return
			}
		}
	}()
	return events, failures
}
func decodeEvent(raw map[string]json.RawMessage) (Event, error) {
	ev := Event{Source: "daemon", Data: map[string]any{}}
	if data, ok := raw["data"]; ok {
		if err := json.Unmarshal(data, &ev.Data); err != nil {
			return ev, err
		}
		if ev.Data == nil {
			ev.Data = map[string]any{}
		}
	}
	for key, value := range raw {
		var dst any
		switch key {
		case "seq":
			dst = &ev.Seq
		case "type":
			dst = &ev.Type
		case "session":
			dst = &ev.Session
		case "window":
			dst = &ev.Window
		case "boot_id":
			dst = &ev.BootID
		case "time":
			var ns int64
			if err := json.Unmarshal(value, &ns); err != nil {
				return ev, err
			}
			ev.At = ns / 1_000_000
			continue
		case "at":
			if _, ok := raw["time"]; ok {
				continue
			}
			dst = &ev.At
		case "data":
			continue
		default:
			var v any
			if err := json.Unmarshal(value, &v); err != nil {
				return ev, err
			}
			ev.Data[key] = v
			continue
		}
		if err := json.Unmarshal(value, dst); err != nil {
			return ev, err
		}
	}
	if ev.Type == "" {
		return ev, errors.New("daemon event is missing type")
	}
	return ev, nil
}

type Presence struct {
	Nonce    string `json:"human_nonce"`
	ClientID string `json:"client_id"`
	Session  string `json:"session"`
	conn     *connection
}

func (c *Client) Presence(ctx context.Context, session string) (*Presence, error) {
	host, _, rawSession := SplitIdentity(session)
	if host != "" {
		session = rawSession
	}
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	if host != "" && host != "local" {
		if err := exchange(conn, "open-host-connection", map[string]any{"host": host, "ssh_auth_sock": os.Getenv("SSH_AUTH_SOCK")}, nil); err != nil {
			conn.Close()
			return nil, contextError(ctx, err)
		}
	}
	p := &Presence{conn: conn}
	if err = exchange(conn, "attach-presence", struct {
		Session string `json:"session,omitempty"`
	}{session}, p); err != nil {
		conn.Close()
		return nil, contextError(ctx, err)
	}
	p.Session = Qualify(host, "session", p.Session)
	return p, nil
}
func (p *Presence) Close() error { return p.conn.Close() }
