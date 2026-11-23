package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/matomeru/ptyhost"
)

const (
	// DefaultApprovalTimeout is how long a blocking PermissionRequest waits before denying.
	DefaultApprovalTimeout = 300 * time.Second
	maxRPCLineBytes        = 16 << 20 // 16 MiB (handles large terminal scrollback frames)
)

// ErrAlreadyRunning is returned by Start when another daemon is already listening on SocketPath.
var ErrAlreadyRunning = errors.New("daemon: already running on socket")

// Config configures a local Matomeru daemon Server.
type Config struct {
	// SocketPath is the Unix domain socket path. Default: DefaultSocketPath().
	SocketPath string
	// MCPAddr is the TCP listen address for the shared HTTP MCP server.
	// Default: "127.0.0.1:0".
	MCPAddr string
	// DisableMCP disables starting the HTTP MCP server even when Store is set.
	DisableMCP bool
	// Store is the shared data.Store for HTTP MCP and the background scheduler.
	// Optional: if nil, PTY hosting and hook telemetry still run.
	Store *data.Store
	// Router is an optional mcpui.Router; if nil and Store != nil, mcpui.New(Store) is used.
	Router *mcpui.Router
	// Host is the PTY host backing panes. Default: ptyhost.NewLocalHost().
	Host *ptyhost.LocalHost
	// ApprovalTimeout bounds blocking approval hook_event calls. Default: 300s.
	ApprovalTimeout time.Duration
	// SchedulerInterval controls how often due schedules are evaluated.
	// Zero uses DefaultSchedulerInterval; negative disables the background ticker.
	SchedulerInterval time.Duration
	// Notifier delivers "remind" schedules. Default: MacOSNotifier{}.
	Notifier Notifier
	// Runner executes "run" and "check" schedules. Default: ShellRunner{}.
	Runner Runner
	// Clock supplies current time (default time.Now).
	Clock func() time.Time
}

type paneMeta struct {
	ID        string
	Cmd       string
	Cwd       string
	Cols      int
	Rows      int
	StartedAt time.Time
}

// Server is Matomeru's self-hosted local Unix-socket daemon.
type Server struct {
	cfg        Config
	socketPath string
	host       *ptyhost.LocalHost
	telemetry  *telemetryStore
	scheduler  *Scheduler
	clock      func() time.Time
	startedAt  time.Time

	mu        sync.Mutex
	panes     map[string]*paneMeta
	unixLn    net.Listener
	httpLn    net.Listener
	httpSrv   *http.Server
	mcpURL    string
	stopSched func()

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	once   sync.Once
}

// New constructs a daemon Server from cfg without starting listeners yet.
func New(cfg Config) (*Server, error) {
	sockPath := cfg.SocketPath
	if sockPath == "" {
		sockPath = DefaultSocketPath()
	}
	host := cfg.Host
	if host == nil {
		host = ptyhost.NewLocalHost()
	}
	if cfg.ApprovalTimeout <= 0 {
		cfg.ApprovalTimeout = DefaultApprovalTimeout
	}
	if cfg.MCPAddr == "" {
		cfg.MCPAddr = "127.0.0.1:0"
	}
	clock := cfg.Clock
	if clock == nil {
		clock = time.Now
	}
	var sched *Scheduler
	if cfg.Store != nil {
		sched = NewScheduler(SchedulerConfig{
			Store:    cfg.Store,
			Notifier: cfg.Notifier,
			Runner:   cfg.Runner,
			Clock:    clock,
		})
	}
	return &Server{
		cfg:        cfg,
		socketPath: sockPath,
		host:       host,
		telemetry:  newTelemetryStore(),
		scheduler:  sched,
		clock:      clock,
		panes:      map[string]*paneMeta{},
	}, nil
}

// SocketPath returns the configured Unix socket path.
func (s *Server) SocketPath() string { return s.socketPath }

// MCPURL returns the base URL of the shared HTTP MCP server (e.g. "http://127.0.0.1:54321"),
// or "" if HTTP MCP is disabled or not started.
func (s *Server) MCPURL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mcpURL
}

// Scheduler returns the daemon's Scheduler (nil when Store was not configured).
func (s *Server) Scheduler() *Scheduler { return s.scheduler }

// Start binds the Unix socket, starts the optional HTTP MCP server and
// background schedule ticker, and begins serving RPC connections.
func (s *Server) Start(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(s.socketPath), 0o700); err != nil {
		return fmt.Errorf("daemon: mkdir socket dir: %w", err)
	}
	// Detect live daemon vs stale socket file.
	if _, err := os.Stat(s.socketPath); err == nil {
		conn, derr := net.DialTimeout("unix", s.socketPath, 300*time.Millisecond)
		if derr == nil {
			_ = conn.Close()
			return fmt.Errorf("%w (%s)", ErrAlreadyRunning, s.socketPath)
		}
		if rerr := os.Remove(s.socketPath); rerr != nil && !os.IsNotExist(rerr) {
			return fmt.Errorf("daemon: remove stale socket: %w", rerr)
		}
	}

	unixLn, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("daemon: listen unix %s: %w", s.socketPath, err)
	}
	_ = os.Chmod(s.socketPath, 0o600)

	s.ctx, s.cancel = context.WithCancel(ctx)
	s.startedAt = s.clock()
	s.unixLn = unixLn

	// Start shared HTTP MCP server when Store is configured and not disabled.
	if s.cfg.Store != nil && !s.cfg.DisableMCP {
		httpLn, err := net.Listen("tcp", s.cfg.MCPAddr)
		if err != nil {
			_ = unixLn.Close()
			_ = os.Remove(s.socketPath)
			return fmt.Errorf("daemon: listen mcp http %s: %w", s.cfg.MCPAddr, err)
		}
		handler := mcpui.NewHTTPHandler(s.cfg.Store, s.cfg.Router)
		httpSrv := &http.Server{Handler: handler}
		s.mu.Lock()
		s.httpLn = httpLn
		s.httpSrv = httpSrv
		s.mcpURL = "http://" + httpLn.Addr().String()
		s.mu.Unlock()

		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			_ = httpSrv.Serve(httpLn)
		}()
	}

	// Start background schedule ticker when Store is configured and interval >= 0.
	if s.scheduler != nil && s.cfg.SchedulerInterval >= 0 {
		s.stopSched = s.scheduler.Start(s.ctx, s.cfg.SchedulerInterval)
	}

	s.wg.Add(1)
	go s.acceptLoop()
	return nil
}

// Serve starts the daemon and blocks until Shutdown is called or ctx is cancelled.
func (s *Server) Serve(ctx context.Context) error {
	if err := s.Start(ctx); err != nil {
		return err
	}
	<-s.ctx.Done()
	return s.Shutdown(context.Background())
}

// Shutdown cleanly stops the Unix listener, HTTP MCP server, scheduler,
// pending approvals, and all live PTY panes. Safe to call multiple times.
func (s *Server) Shutdown(ctx context.Context) error {
	s.once.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		if s.stopSched != nil {
			s.stopSched()
		}
		s.telemetry.reset("daemon shutting down")

		s.mu.Lock()
		unixLn := s.unixLn
		httpSrv := s.httpSrv
		s.panes = map[string]*paneMeta{}
		s.mu.Unlock()

		if unixLn != nil {
			_ = unixLn.Close()
		}
		if httpSrv != nil {
			shutCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			_ = httpSrv.Shutdown(shutCtx)
			cancel()
		}
		s.host.CloseAll()
		_ = os.Remove(s.socketPath)
		s.wg.Wait()
	})
	return nil
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.unixLn.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go func(c net.Conn) {
			defer s.wg.Done()
			s.serveConn(c)
		}(conn)
	}
}

func (s *Server) serveConn(conn net.Conn) {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 64*1024), maxRPCLineBytes)
	enc := json.NewEncoder(conn)

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			_ = enc.Encode(Response{OK: false, Error: "invalid JSON request: " + err.Error()})
			continue
		}
		resp, shouldShutdown := s.handleRequest(req)
		if err := enc.Encode(resp); err != nil {
			if shouldShutdown {
				go func() { _ = s.Shutdown(context.Background()) }()
			}
			return
		}
		if shouldShutdown {
			go func() { _ = s.Shutdown(context.Background()) }()
			return
		}
	}
}

func (s *Server) handleRequest(req Request) (Response, bool) {
	id := req.ID
	if id == "" {
		id = req.PaneID
	}

	switch req.Op {
	case OpStatus:
		s.mu.Lock()
		count := len(s.panes)
		mcpURL := s.mcpURL
		s.mu.Unlock()
		up := s.clock().Sub(s.startedAt)
		if up < 0 {
			up = 0
		}
		return Response{
			OK:        true,
			PID:       os.Getpid(),
			Socket:    s.socketPath,
			MCPURL:    mcpURL,
			PaneCount: count,
			Uptime:    up.Round(time.Millisecond).String(),
			UptimeSec: up.Seconds(),
		}, false

	case OpOpen:
		if id == "" {
			return Response{OK: false, Error: "open: id is required"}, false
		}
		cols, rows := req.Cols, req.Rows
		if cols <= 0 {
			cols = 80
		}
		if rows <= 0 {
			rows = 24
		}
		env := map[string]string{}
		maps.Copy(env, req.Env)
		// ALWAYS inject MTM_PANE_ID, MTM_SOCKET, and MTM_MCP_URL into every launched pane.
		env[EnvPaneID] = id
		env[EnvSocket] = s.socketPath
		if mcpBase := s.MCPURL(); mcpBase != "" {
			env[EnvMCPURL] = mcpBase + "/mcp/" + url.PathEscape(id)
		}
		if err := s.host.OpenWithEnv(id, req.Cmd, req.Cwd, cols, rows, env); err != nil {
			return Response{OK: false, Error: err.Error()}, false
		}
		s.mu.Lock()
		s.panes[id] = &paneMeta{
			ID:        id,
			Cmd:       req.Cmd,
			Cwd:       req.Cwd,
			Cols:      cols,
			Rows:      rows,
			StartedAt: s.clock(),
		}
		s.mu.Unlock()
		return Response{OK: true}, false

	case OpWrite:
		if id == "" {
			return Response{OK: false, Error: "write: id is required"}, false
		}
		if err := s.host.Write(id, req.Data); err != nil {
			return Response{OK: false, Error: err.Error()}, false
		}
		return Response{OK: true}, false

	case OpResize:
		if id == "" {
			return Response{OK: false, Error: "resize: id is required"}, false
		}
		if err := s.host.Resize(id, req.Cols, req.Rows); err != nil {
			return Response{OK: false, Error: err.Error()}, false
		}
		s.mu.Lock()
		if meta := s.panes[id]; meta != nil {
			meta.Cols = max(req.Cols, 1)
			meta.Rows = max(req.Rows, 1)
		}
		s.mu.Unlock()
		return Response{OK: true}, false

	case OpSnapshot:
		if id == "" {
			return Response{OK: false, Error: "snapshot: id is required"}, false
		}
		frame, ok := s.host.Snapshot(id)
		if !ok {
			return Response{OK: true, Found: false}, false
		}
		return Response{OK: true, Found: true, Frame: &frame}, false

	case OpList:
		return Response{OK: true, Panes: s.listPanes()}, false

	case OpClose:
		if id == "" {
			return Response{OK: false, Error: "close: id is required"}, false
		}
		err := s.host.Close(id)
		s.mu.Lock()
		delete(s.panes, id)
		s.mu.Unlock()
		s.telemetry.clearPane(id)
		if err != nil {
			return Response{OK: false, Error: err.Error()}, false
		}
		return Response{OK: true}, false

	case OpReset:
		s.host.CloseAll()
		s.mu.Lock()
		s.panes = map[string]*paneMeta{}
		s.mu.Unlock()
		s.telemetry.reset("daemon reset")
		return Response{OK: true}, false

	case OpShutdown:
		return Response{OK: true}, true

	case OpHookEvent:
		ev := HookEvent{
			PaneID:         req.PaneID,
			Harness:        req.Harness,
			Kind:           req.Kind,
			State:          req.State,
			Tool:           req.Tool,
			Summary:        req.Summary,
			Files:          req.Files,
			Prompt:         req.Prompt,
			Response:       req.Response,
			SubagentID:     req.SubagentID,
			SubagentAction: req.SubagentAction,
			RequestID:      req.RequestID,
			Payload:        req.Payload,
			TimeoutMs:      req.TimeoutMs,
		}
		if ev.PaneID == "" {
			ev.PaneID = req.ID
		}
		if ev.Kind == HookKindApproval {
			timeout := s.cfg.ApprovalTimeout
			if ev.TimeoutMs > 0 {
				timeout = time.Duration(ev.TimeoutMs) * time.Millisecond
			}
			res := s.telemetry.waitApproval(s.ctx, ev, timeout, s.clock())
			return Response{
				OK:       true,
				Decision: res.Decision,
				Reason:   res.Reason,
			}, false
		}
		s.telemetry.recordEvent(ev, s.clock())
		if s.cfg.Store != nil {
			s.indexHookEvent(ev)
		}
		return Response{OK: true}, false

	case OpResolveApproval:
		if err := s.telemetry.resolveApproval(req.RequestID, req.Decision, req.Reason); err != nil {
			return Response{OK: false, Error: err.Error()}, false
		}
		return Response{OK: true}, false

	case OpTelemetry:
		tel, pending := s.telemetry.snapshot(id)
		return Response{
			OK:               true,
			Telemetry:        tel,
			PendingApprovals: pending,
		}, false

	default:
		return Response{OK: false, Error: fmt.Sprintf("unknown op %q", req.Op)}, false
	}
}

func (s *Server) listPanes() []PaneInfo {
	s.mu.Lock()
	metas := make([]paneMeta, 0, len(s.panes))
	for _, m := range s.panes {
		metas = append(metas, *m)
	}
	s.mu.Unlock()

	slices.SortFunc(metas, func(a, b paneMeta) int {
		if c := a.StartedAt.Compare(b.StartedAt); c != 0 {
			return c
		}
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})

	out := make([]PaneInfo, 0, len(metas))
	for _, m := range metas {
		info := PaneInfo{
			ID:        m.ID,
			Cmd:       m.Cmd,
			Cwd:       m.Cwd,
			Cols:      m.Cols,
			Rows:      m.Rows,
			StartedAt: m.StartedAt,
		}
		if f, ok := s.host.Snapshot(m.ID); ok {
			info.PID = f.PID
			info.Title = f.Title
			info.Cols = f.Cols
			info.Rows = f.Rows
			info.Exited = f.Exited
			info.ExitCode = f.ExitCode
		}
		out = append(out, info)
	}
	return out
}
