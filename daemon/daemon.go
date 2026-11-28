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
	"sync/atomic"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/matomeru/mdweb"
	"ofam.dev/matomeru/panesearch"
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
	// RefreshSchemas updates the shared registry from user configuration.
	RefreshSchemas func() error
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
	// QueueDispatcher delivers "agent_queue" schedules. Default: ServerQueueDispatcher.
	QueueDispatcher QueueDispatcher
	// Clock supplies current time (default time.Now).
	Clock func() time.Time
	// HandoffPath optionally overrides $MTM_DAEMON_HANDOFF when adopting an
	// upgraded daemon's live PTY master FDs and listeners.
	HandoffPath string
	// ExecFunc overrides syscall.Exec during OpUpgrade (for tests).
	ExecFunc func(argv0 string, argv []string, envv []string) error
	// HooksPath is the declarative callback rules file (hooks.json), re-read
	// on every agent.done event. Empty disables file rules; in-memory rules
	// registered over RPC still fire.
	HooksPath string
	// WebIndexPath optionally overrides the persistent JSON index file for the
	// daemon's /md web viewer. Default: mdweb-index.json alongside SocketPath.
	WebIndexPath string
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
	callbacks  *callbackBus
	wakes      *wakeQueue
	scheduler  *Scheduler
	clock      func() time.Time
	startedAt  time.Time
	generation int // exec generation: 1 fresh, handoff+1 after an upgrade

	mu        sync.Mutex
	panes     map[string]*paneMeta
	unixLn    net.Listener
	httpLn    net.Listener
	httpSrv   *http.Server
	mdWeb     *mdweb.Handler
	mcpURL    string
	stopSched func()
	// queueWatch hands queued work to agent panes that watch a queue; nil
	// without a Store.
	queueWatch *queueWatcher
	// titles gives a pane its one automatic title from its first prompt.
	titles *paneTitleSummarizer

	subsMu sync.Mutex
	subs   map[*frameSubscriber]struct{}

	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	once      sync.Once
	upgrading atomic.Bool
}

type frameSubscriber struct {
	mu     sync.Mutex
	dirty  map[string]struct{}
	notify chan struct{}
}

type scrollSig struct {
	n     int
	ptr   *string
	first string
	last  string
}

func makeScrollSig(sb []string) scrollSig {
	if len(sb) == 0 {
		return scrollSig{}
	}
	return scrollSig{
		n:     len(sb),
		ptr:   &sb[0],
		first: sb[0],
		last:  sb[len(sb)-1],
	}
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
	srv := &Server{
		cfg:        cfg,
		socketPath: sockPath,
		host:       host,
		telemetry:  newTelemetryStore(),
		callbacks:  newCallbackBus(cfg.HooksPath, clock),
		clock:      clock,
		panes:      map[string]*paneMeta{},
		subs:       map[*frameSubscriber]struct{}{},
	}
	srv.wireMailbox()
	srv.titles = newPaneTitleSummarizer(cfg.Store)
	srv.telemetry.onDone = srv.onAgentEdge
	if cfg.Store != nil {
		srv.callbacks.enrich = srv.enrichAgentDone
		disp := cfg.QueueDispatcher
		if disp == nil {
			disp = NewServerQueueDispatcher(srv)
		}
		srv.queueWatch = newQueueWatcher(cfg.Store, disp, clock, srv.paneReadyForQueue)
		srv.scheduler = NewScheduler(SchedulerConfig{
			Store:      cfg.Store,
			Notifier:   cfg.Notifier,
			Runner:     cfg.Runner,
			Dispatcher: disp,
			Clock:      clock,
			AfterTick:  srv.queueWatch.pollScheduled,
		})
	}
	prevOnChange := host.OnChange
	host.OnChange = func(id string) {
		if prevOnChange != nil {
			prevOnChange(id)
		}
		srv.notifyPaneChanged(id)
	}
	return srv, nil
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

// Start binds the Unix socket (or adopts inherited FDs from a zero-downtime
// upgrade handoff), starts the optional HTTP MCP server and background
// schedule ticker, and begins serving RPC connections.
func (s *Server) Start(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(s.socketPath), 0o700); err != nil {
		return fmt.Errorf("daemon: mkdir socket dir: %w", err)
	}

	handoffPath := s.cfg.HandoffPath
	if handoffPath == "" {
		if envPath := os.Getenv(EnvDaemonHandoff); envPath != "" {
			handoffPath = envPath
			_ = os.Unsetenv(EnvDaemonHandoff)
		}
	}

	var handoff *daemonHandoff
	if handoffPath != "" {
		var err error
		handoff, err = s.loadHandoff(handoffPath)
		if err != nil {
			return err
		}
	}

	var unixLn net.Listener
	if handoff != nil && handoff.UnixFD >= 0 {
		var err error
		unixLn, err = adoptFileListener(handoff.UnixFD, "unix-listener")
		if err != nil {
			return err
		}
		if ul, ok := unixLn.(*net.UnixListener); ok {
			ul.SetUnlinkOnClose(true)
		}
	} else {
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

		var err error
		unixLn, err = net.Listen("unix", s.socketPath)
		if err != nil {
			return fmt.Errorf("daemon: listen unix %s: %w", s.socketPath, err)
		}
		_ = os.Chmod(s.socketPath, 0o600)
	}

	s.ctx, s.cancel = context.WithCancel(ctx)
	s.startedAt = s.clock()
	s.generation = 1
	if handoff != nil {
		s.generation = handoff.Generation + 1
		if !handoff.StartedAt.IsZero() {
			s.startedAt = handoff.StartedAt
		}
	}
	s.unixLn = unixLn
	s.callbacks.start(s.ctx, &s.wg)
	s.queueWatch.start(s.ctx, &s.wg)

	// Restore inherited panes, PTY sessions, and telemetry before serving.
	if handoff != nil {
		if err := s.host.AdoptFromUpgrade(handoff.Sessions); err != nil {
			_ = unixLn.Close()
			return err
		}
		s.mu.Lock()
		for _, m := range handoff.Panes {
			mc := m
			s.panes[m.ID] = &mc
		}
		s.mu.Unlock()
		s.telemetry.restore(handoff.Telemetry)
		s.callbacks.restore(handoff.Hooks)
	}

	// Start shared HTTP MCP server when Store is configured and not disabled.
	if s.cfg.Store != nil && !s.cfg.DisableMCP {
		var httpLn net.Listener
		var err error
		if handoff != nil && handoff.MCPFD >= 0 {
			httpLn, err = adoptFileListener(handoff.MCPFD, "mcp-listener")
		} else {
			httpLn, err = net.Listen("tcp", s.cfg.MCPAddr)
		}
		if err != nil {
			_ = unixLn.Close()
			if handoff == nil {
				_ = os.Remove(s.socketPath)
			}
			return fmt.Errorf("daemon: listen mcp http %s: %w", s.cfg.MCPAddr, err)
		}
		box := mcpui.NewToolbox(s.cfg.Store, s.cfg.Router)
		box.RefreshSchemas = s.cfg.RefreshSchemas
		if box.PaneSearch != nil {
			box.PaneSearch.LiveData = s.paneSearchLiveData
		}
		if box.Panes != nil {
			box.Panes.Runtime = NewServerPaneRuntime(s)
			box.Panes.ResolveLaunch = func(harnessID, mcpURL, paneID, customCmd string) (string, map[string]string, error) {
				return ResolveHarnessLaunch(DefaultConfigDir(), harnessID, mcpURL, paneID, customCmd)
			}
			box.Panes.ResolveLaunchWithPrompt = func(harnessID, mcpURL, paneID, customCmd, prompt string) (mcpui.PaneLaunchSpec, error) {
				cmd, env, delivered, err := ResolveHarnessLaunchWithPrompt(DefaultConfigDir(), harnessID, mcpURL, paneID, customCmd, prompt)
				return mcpui.PaneLaunchSpec{Cmd: cmd, Env: env, PromptDeliveredInCmd: delivered}, err
			}
			box.Panes.MCPURL = func(paneID string) string {
				if base := s.MCPURL(); base != "" {
					return base + "/mcp/" + url.PathEscape(paneID)
				}
				return ""
			}
		}
		mcpHandler := mcpui.NewHTTPHandlerWithToolbox(box)
		webIndexPath := s.cfg.WebIndexPath
		if webIndexPath == "" && s.socketPath != "" {
			webIndexPath = filepath.Join(filepath.Dir(s.socketPath), "mdweb-index.json")
		}
		mdHandler := mdweb.NewHandler(mdweb.Config{
			BasePath:          "/md",
			AllowAnyLocalFile: true,
			IndexPath:         webIndexPath,
		})
		mux := http.NewServeMux()
		mux.Handle("/md", http.RedirectHandler("/md/", http.StatusMovedPermanently))
		mux.Handle("/md/", mdHandler)
		mux.Handle("/", mcpHandler)
		httpSrv := &http.Server{Handler: mux}
		s.mu.Lock()
		s.httpLn = httpLn
		s.httpSrv = httpSrv
		s.mdWeb = mdHandler
		s.mcpURL = "http://" + httpLn.Addr().String()
		s.mu.Unlock()

		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			_ = httpSrv.Serve(httpLn)
		}()
	} else if handoff != nil && handoff.MCPFD >= 0 {
		_ = os.NewFile(uintptr(handoff.MCPFD), "unused-mcp-listener").Close()
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
	if s.upgrading.Load() {
		// execUpgrade is replacing this process in place; wait for syscall.Exec
		// without closing PTYs or removing the socket.
		select {}
	}
	return s.Shutdown(context.Background())
}

// Shutdown cleanly stops the Unix listener, HTTP MCP server, scheduler,
// pending approvals, and all live PTY panes. Safe to call multiple times.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.upgrading.Load() {
		return nil
	}
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
		mdWeb := s.mdWeb
		s.panes = map[string]*paneMeta{}
		s.mu.Unlock()

		if mdWeb != nil {
			mdWeb.Close()
		}

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
		if req.Op == OpSubscribe {
			s.serveSubscribe(conn, enc)
			return
		}
		if req.Op == OpWatchEvents {
			s.serveWatchEvents(conn, enc, req.PaneID)
			return
		}
		if req.Op == OpUpgrade {
			bin, err := resolveUpgradeBinary(req.Binary)
			if err != nil {
				_ = enc.Encode(Response{OK: false, Error: err.Error()})
				continue
			}
			handoffPath, keepFiles, paneCount, err := s.ExportHandoff()
			if err != nil {
				_ = enc.Encode(Response{OK: false, Error: err.Error()})
				continue
			}
			_ = enc.Encode(Response{
				OK:        true,
				PID:       os.Getpid(),
				Socket:    s.socketPath,
				MCPURL:    s.MCPURL(),
				PaneCount: paneCount, Generation: s.generation,
			})
			_ = conn.Close()
			go s.execUpgrade(bin, handoffPath, keepFiles)
			return
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

const subscribeCoalesceWindow = 8 * time.Millisecond

func (s *Server) notifyPaneChanged(id string) {
	if id == "" {
		return
	}
	s.subsMu.Lock()
	for sub := range s.subs {
		sub.mark(id)
	}
	s.subsMu.Unlock()
}

func (sub *frameSubscriber) mark(id string) {
	sub.mu.Lock()
	sub.dirty[id] = struct{}{}
	sub.mu.Unlock()
	select {
	case sub.notify <- struct{}{}:
	default:
	}
}

func (sub *frameSubscriber) drain() []string {
	sub.mu.Lock()
	if len(sub.dirty) == 0 {
		sub.mu.Unlock()
		return nil
	}
	ids := make([]string, 0, len(sub.dirty))
	for id := range sub.dirty {
		ids = append(ids, id)
	}
	clear(sub.dirty)
	sub.mu.Unlock()
	slices.Sort(ids)
	return ids
}

func (s *Server) serveSubscribe(conn net.Conn, enc *json.Encoder) {
	// Ack subscription immediately so clients can detect support vs older daemons.
	if err := enc.Encode(Response{OK: true}); err != nil {
		return
	}

	sub := &frameSubscriber{
		dirty:  map[string]struct{}{},
		notify: make(chan struct{}, 1),
	}

	s.subsMu.Lock()
	s.subs[sub] = struct{}{}
	s.subsMu.Unlock()
	defer func() {
		s.subsMu.Lock()
		delete(s.subs, sub)
		s.subsMu.Unlock()
	}()

	// Seed subscriber with currently open panes so its cache warms immediately.
	s.mu.Lock()
	for id := range s.panes {
		sub.mark(id)
	}
	s.mu.Unlock()

	// Detect client disconnect promptly.
	clientDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		var buf [64]byte
		for {
			if _, err := conn.Read(buf[:]); err != nil {
				return
			}
		}
	}()
	defer func() {
		_ = conn.Close()
		<-clientDone
	}()

	lastScroll := map[string]scrollSig{}
	var lastPush time.Time
	var timer *time.Timer
	var timerC <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	flush := func() bool {
		ids := sub.drain()
		if len(ids) == 0 {
			return true
		}
		for _, id := range ids {
			frame, ok := s.host.Snapshot(id)
			if !ok {
				delete(lastScroll, id)
				if err := enc.Encode(Response{OK: true, Found: false, PaneID: id}); err != nil {
					return false
				}
				continue
			}
			sig := makeScrollSig(frame.Scrollback)
			unchanged := false
			if prev, have := lastScroll[id]; have && prev == sig {
				frame.Scrollback = nil
				unchanged = true
			} else {
				lastScroll[id] = sig
			}
			if err := enc.Encode(Response{
				OK:                  true,
				Found:               true,
				PaneID:              id,
				Frame:               &frame,
				ScrollbackUnchanged: unchanged,
			}); err != nil {
				return false
			}
		}
		lastPush = time.Now()
		return true
	}

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-clientDone:
			return
		case <-sub.notify:
			since := time.Since(lastPush)
			if lastPush.IsZero() || since >= subscribeCoalesceWindow {
				if timer != nil {
					timer.Stop()
					timerC = nil
				}
				if !flush() {
					return
				}
			} else if timerC == nil {
				wait := subscribeCoalesceWindow - since
				if timer == nil {
					timer = time.NewTimer(wait)
				} else {
					timer.Reset(wait)
				}
				timerC = timer.C
			}
		case <-timerC:
			timerC = nil
			if !flush() {
				return
			}
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
		bi := CurrentBuildInfo()
		return Response{
			OK:        true,
			PID:       os.Getpid(),
			Socket:    s.socketPath,
			MCPURL:    mcpURL,
			PaneCount: count, Generation: s.generation,
			Uptime:    up.Round(time.Millisecond).String(),
			UptimeSec: up.Seconds(),
			Version:   bi.Version,
			Commit:    bi.Commit,
			Dirty:     bi.Dirty,
			BuiltAt:   bi.BuiltAt,
			Binary:    bi.Binary,
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
		s.notifyPaneChanged(id)
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
		s.notifyPaneChanged(id)
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
		s.forgetPaneHooks(id)
		s.notifyPaneChanged(id)
		if err != nil {
			return Response{OK: false, Error: err.Error()}, false
		}
		return Response{OK: true}, false

	case OpReset:
		s.mu.Lock()
		closedIDs := make([]string, 0, len(s.panes))
		for pid := range s.panes {
			closedIDs = append(closedIDs, pid)
		}
		s.panes = map[string]*paneMeta{}
		s.mu.Unlock()
		s.host.CloseAll()
		s.telemetry.reset("daemon reset")
		for _, pid := range closedIDs {
			s.forgetPaneHooks(pid)
			s.notifyPaneChanged(pid)
		}
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
			FinalResponse:  req.FinalResponse,
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
		s.summarizePaneTitle(ev)
		return Response{OK: true}, false

	case OpResolveApproval:
		if err := s.telemetry.resolveApproval(req.RequestID, req.Decision, req.Reason); err != nil {
			return Response{OK: false, Error: err.Error()}, false
		}
		return Response{OK: true}, false

	case OpHookRegister, OpHookUnregister, OpHookList, OpHookLog:
		return s.handleHookRequest(req), false

	case OpMailNotify:
		return s.handleMailRequest(req), false

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

func (s *Server) paneSearchLiveData() mcpui.PaneSearchLiveSnapshot {
	snap := mcpui.PaneSearchLiveSnapshot{
		Frames:          map[string]panesearch.LiveFrame{},
		Turns:           map[string][]panesearch.Turn{},
		TranscriptPaths: map[string]string{},
		SessionIDs:      map[string]string{},
	}
	s.mu.Lock()
	ids := make([]string, 0, len(s.panes))
	for id := range s.panes {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		if f, ok := s.host.Snapshot(id); ok {
			snap.Frames[id] = panesearch.LiveFrame{
				Lines:    f.History(),
				Revision: f.Revision,
			}
		}
	}
	tel, _ := s.telemetry.snapshot("")
	for id, pt := range tel {
		if pt == nil {
			continue
		}
		if pt.TranscriptPath != "" {
			snap.TranscriptPaths[id] = pt.TranscriptPath
		}
		if pt.SessionID != "" {
			snap.SessionIDs[id] = pt.SessionID
		}
		if len(pt.RecentTurns) > 0 {
			turns := make([]panesearch.Turn, len(pt.RecentTurns))
			for i, ht := range pt.RecentTurns {
				turns[i] = panesearch.Turn{
					Prompt:   ht.Prompt,
					Response: ht.Response,
					Summary:  ht.Summary,
					At:       ht.At,
				}
			}
			snap.Turns[id] = turns
		}
	}
	return snap
}
