//go:build darwin || linux

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func registryDatabase(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "selected.db")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func registryRecords(t *testing.T, data string) map[string]mcpServerRecord {
	t.Helper()
	dir := data + ".mcp/servers"
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]mcpServerRecord{}
	}
	if err != nil {
		t.Fatal(err)
	}
	records := make(map[string]mcpServerRecord)
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var record mcpServerRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			t.Fatalf("invalid record %s: %v", entry.Name(), err)
		}
		if record.Version != 1 || len(record.LaunchID) != 32 || entry.Name() != record.LaunchID+".json" {
			t.Fatalf("invalid launch identity: %s: %+v", entry.Name(), record)
		}
		records[entry.Name()] = record
	}
	return records
}

func TestMCPRegistryExclusivePrivateAndOwnCleanup(t *testing.T) {
	t.Parallel()
	data := registryDatabase(t)
	alias := filepath.Join(t.TempDir(), "alias.db")
	if err := os.Symlink(data, alias); err != nil {
		t.Fatal(err)
	}
	config := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cwd, alias)
	if err != nil {
		t.Fatal(err)
	}
	remove, err := registerMCP(relative, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = remove() })
	canonical, err := filepath.EvalSymlinks(data)
	if err != nil {
		t.Fatal(err)
	}
	resolvedConfig, err := filepath.EvalSymlinks(config)
	if err != nil {
		t.Fatal(err)
	}
	records := registryRecords(t, canonical)
	if len(records) != 1 {
		t.Fatalf("records: %+v", records)
	}
	for name, record := range records {
		if record.DataPath != canonical || record.ConfigDir != resolvedConfig || record.Server.PID != os.Getpid() || record.Server.StartID == "" || record.Server.StartSource == "" || record.Server.StartError != "" {
			t.Fatalf("scope/server identity: %+v", record)
		}
		if record.Launcher.PID != os.Getppid() || record.LauncherSource != "os.Getppid at registration" || !strings.HasPrefix(record.ClientOwnership, "unverified:") {
			t.Fatalf("launcher provenance: %+v", record)
		}
		for path, want := range map[string]os.FileMode{
			canonical + ".mcp":                 0o700,
			canonical + ".mcp/servers":         0o700,
			canonical + ".mcp/servers/" + name: 0o600,
		} {
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != want {
				t.Fatalf("permissions %s: %v, %v; want %o", path, info, err, want)
			}
		}
	}
	if _, err := os.Lstat(alias + ".mcp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("registry followed database alias instead of canonical file: %v", err)
	}
	missing := mcpIdentifyProcess(-1)
	if missing.StartID != "" || missing.StartSource != "" || missing.StartError == "" {
		t.Fatalf("failed process lookup invented identity: %+v", missing)
	}
	if err := remove(); err != nil || len(registryRecords(t, canonical)) != 0 {
		t.Fatalf("own cleanup: %v", err)
	}

	for _, replacement := range []string{"file", "symlink"} {
		t.Run(replacement, func(t *testing.T) {
			dir, err := mcpRegistryDirectory(canonical)
			if err != nil {
				t.Fatal(err)
			}
			defer dir.Close()
			occupied := filepath.Join(canonical+".mcp/servers", strings.Repeat("00", 16)+".json")
			victim := filepath.Join(t.TempDir(), "untouched")
			const sentinel = "another launch's bytes"
			if err := os.WriteFile(victim, []byte(sentinel), 0o600); err != nil {
				t.Fatal(err)
			}
			if replacement == "symlink" {
				err = os.Symlink(victim, occupied)
			} else {
				err = os.WriteFile(occupied, []byte(sentinel), 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(occupied)
			entropy := append(make([]byte, 16), bytes.Repeat([]byte{1}, 16)...)
			cleanup, err := createMCPRecord(dir, mcpServerRecord{Version: 1}, bytes.NewReader(entropy))
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			own := filepath.Join(canonical+".mcp/servers", strings.Repeat("01", 16)+".json")
			if _, err := os.Stat(own); err != nil {
				t.Fatalf("collision did not choose a new id: %v", err)
			}
			// Replace the created entry, including the inode-reuse case.
			if err := os.Remove(own); err != nil {
				t.Fatal(err)
			}
			if replacement == "symlink" {
				err = os.Symlink(victim, own)
			} else {
				err = os.WriteFile(own, []byte(sentinel), 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(own)
			if err := cleanup(); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{occupied, own, victim} {
				raw, err := os.ReadFile(path)
				if err != nil || string(raw) != sentinel {
					t.Fatalf("changed another entry %s: %q %v", path, raw, err)
				}
			}
		})
	}
}

func TestMCPRegistryRejectsUnsafeDirectories(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"root symlink", "servers symlink", "root public", "servers public"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			data := registryDatabase(t)
			victim := t.TempDir()
			root := data + ".mcp"
			target := root
			if strings.HasPrefix(kind, "servers") {
				if err := os.Mkdir(root, 0o700); err != nil {
					t.Fatal(err)
				}
				target = filepath.Join(root, "servers")
			}
			var err error
			if strings.HasSuffix(kind, "symlink") {
				err = os.Symlink(victim, target)
			} else {
				err = os.Mkdir(target, 0o755)
				if err == nil {
					err = os.Chmod(target, 0o755)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if cleanup, err := registerMCP(data, filepath.Dir(data)); err == nil {
				_ = cleanup()
				t.Fatal("unsafe registry directory was accepted")
			}
			entries, err := os.ReadDir(victim)
			if err != nil || len(entries) != 0 {
				t.Fatalf("followed registry symlink: %v %v", entries, err)
			}
			if strings.HasSuffix(kind, "public") {
				info, err := os.Stat(target)
				if err != nil || info.Mode().Perm() != 0o755 {
					t.Fatalf("changed existing directory permissions: %v %v", info, err)
				}
			}
		})
	}
	t.Run("replaced pinned directory", func(t *testing.T) {
		t.Parallel()
		data := registryDatabase(t)
		remove, err := registerMCP(data, filepath.Dir(data))
		if err != nil {
			t.Fatal(err)
		}
		defer remove()
		canonical, _ := filepath.EvalSymlinks(data)
		records := registryRecords(t, canonical)
		servers := canonical + ".mcp/servers"
		saved := canonical + ".mcp/original"
		if err := os.Rename(servers, saved); err != nil {
			t.Fatal(err)
		}
		victim := t.TempDir()
		if err := os.Symlink(victim, servers); err != nil {
			t.Fatal(err)
		}
		for name := range records {
			path := filepath.Join(victim, name)
			if err := os.WriteFile(path, []byte("untouched replacement"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := remove(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(saved, name)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("own record remained in the pinned directory: %v", err)
			}
			raw, err := os.ReadFile(path)
			if err != nil || string(raw) != "untouched replacement" {
				t.Fatalf("followed replaced registry directory: %q %v", raw, err)
			}
		}
	})
}

type mcpObservedReader struct {
	io.Reader
	observe func()
}

func (r mcpObservedReader) Read(p []byte) (int, error) {
	r.observe()
	return r.Reader.Read(p)
}

type mcpFailedIO struct{}

func (mcpFailedIO) Read([]byte) (int, error)  { return 0, io.ErrUnexpectedEOF }
func (mcpFailedIO) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestMCPRegistryServeLifecycle(t *testing.T) {
	t.Parallel()
	for _, command := range []string{"tools", "instructions", "whoami", "config"} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			app, _, _ := mcpApp(t, nil)
			if err := app.commands([]string{"mcp", command}, strings.NewReader(""), io.Discard, io.Discard, nil).Run(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(app.dataPath + ".mcp"); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("non-serve command created a registry: %v", err)
			}
		})
	}
	for _, tc := range []struct {
		name string
		in   io.Reader
		out  io.Writer
		want error
	}{
		{"EOF", strings.NewReader(""), io.Discard, nil},
		{"read error", mcpFailedIO{}, io.Discard, io.ErrUnexpectedEOF},
		{"write error", strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ping\"}\n"), mcpFailedIO{}, io.ErrClosedPipe},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			app, _, _ := mcpApp(t, nil)
			data, _ := filepath.EvalSymlinks(app.dataPath)
			in := mcpObservedReader{Reader: tc.in, observe: func() {
				if len(registryRecords(t, data)) != 1 {
					t.Fatal("connection served before registration")
				}
			}}
			err := app.commands([]string{"mcp", "serve"}, in, tc.out, io.Discard, nil).Run(context.Background())
			if !errors.Is(err, tc.want) || len(registryRecords(t, data)) != 0 {
				t.Fatalf("serve cleanup: %v; want %v", err, tc.want)
			}
		})
	}
	t.Run("cancel blocked stdin", func(t *testing.T) {
		t.Parallel()
		app, _, _ := mcpApp(t, nil)
		in, client, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		defer in.Close()
		defer client.Close()
		out, replies := io.Pipe()
		defer out.Close()
		defer replies.Close()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan struct{})
		var serveErr error
		go func() {
			serveErr = app.commands([]string{"mcp", "serve"}, in, replies, io.Discard, nil).Run(ctx)
			replies.Close()
			close(done)
		}()
		t.Cleanup(func() {
			cancel()
			in.Close()
			replies.Close()
			<-done
		})
		fmt.Fprintln(client, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
		if _, err := bufio.NewReader(out).ReadString('\n'); err != nil {
			<-done
			t.Fatalf("ping: %v; serve: %v", err, serveErr)
		}
		cancel()
		select {
		case <-done:
			data, _ := filepath.EvalSymlinks(app.dataPath)
			if !errors.Is(serveErr, context.Canceled) || len(registryRecords(t, data)) != 0 {
				t.Fatalf("cancel cleanup: %v", serveErr)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("context cancellation left serve blocked on stdin")
		}
	})
}

// Subprocess isolation lets the actual stdio entry point install signal handlers
// without mutating signal state in this package's parallel test process.
func TestMCPRegistryHelperProcess(t *testing.T) {
	t.Parallel()
	data := os.Getenv("MTM_REGISTRY_TEST_DATA")
	if data == "" {
		t.Skip("subprocess helper")
	}
	err := run([]string{"--data", data, "--config", filepath.Dir(data), "--socket", filepath.Join(filepath.Dir(data), "dead.sock"), "mcp", "serve"}, os.Stdin, os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestMCPRegistryIndependentProcesses(t *testing.T) {
	t.Parallel()
	data := filepath.Join(t.TempDir(), "selected.db")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	type child struct {
		cmd     *exec.Cmd
		input   io.WriteCloser
		done    chan struct{}
		waitErr error
		stderr  bytes.Buffer
		record  mcpServerRecord
		name    string
	}
	start := func(wrapper bool) *child {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		cmd := exec.CommandContext(ctx, exe, "-test.run=^TestMCPRegistryHelperProcess$")
		if wrapper {
			cmd = exec.CommandContext(ctx, "/bin/sh", "-c", `"$1" -test.run=^TestMCPRegistryHelperProcess$; status=$?; exit "$status"`, "registry-wrapper", exe)
		}
		cmd.Env = append(os.Environ(), "MTM_REGISTRY_TEST_DATA="+data)
		c := &child{cmd: cmd, done: make(chan struct{})}
		cmd.Stderr = &c.stderr
		c.input, err = cmd.StdinPipe()
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		output, err := cmd.StdoutPipe()
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			cancel()
			t.Fatal(err)
		}
		go func() { c.waitErr = cmd.Wait(); close(c.done) }()
		t.Cleanup(func() {
			_ = c.input.Close()
			cancel()
			<-c.done
		})
		fmt.Fprintln(c.input, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
		line, err := bufio.NewReader(output).ReadString('\n')
		if err != nil || !json.Valid([]byte(line)) {
			t.Fatalf("initialize stdout %q: %v", line, err)
		}
		canonical, err := filepath.EvalSymlinks(data)
		if err != nil {
			t.Fatal(err)
		}
		for name, record := range registryRecords(t, canonical) {
			if !wrapper && record.Server.PID == cmd.Process.Pid || wrapper && record.Launcher.PID == cmd.Process.Pid {
				c.record, c.name = record, name
			}
		}
		if c.name == "" {
			t.Fatal("live subprocess has no launch record")
		}
		birth := mcpIdentifyProcess(c.record.Server.PID)
		if birth.StartError != "" || birth != c.record.Server {
			t.Fatalf("record does not match live OS process birth: got %+v, OS %+v", c.record.Server, birth)
		}
		launcherPID := os.Getpid()
		if wrapper {
			launcherPID = cmd.Process.Pid
		}
		launcher := mcpIdentifyProcess(launcherPID)
		if c.record.Launcher != launcher || !strings.HasPrefix(c.record.ClientOwnership, "unverified:") {
			t.Fatalf("launcher mistaken for a verified client: %+v; OS %+v", c.record, launcher)
		}
		return c
	}
	finish := func(c *child, sig syscall.Signal) {
		t.Helper()
		if sig == 0 {
			_ = c.input.Close()
		} else if err := c.cmd.Process.Signal(sig); err != nil {
			t.Fatal(err)
		}
		select {
		case <-c.done:
		case <-time.After(5 * time.Second):
			t.Fatal("fixture server did not terminate")
		}
		if sig == 0 && c.waitErr != nil {
			t.Fatalf("EOF exit: %v; %s", c.waitErr, c.stderr.String())
		}
		if sig != 0 {
			var exit *exec.ExitError
			if !errors.As(c.waitErr, &exit) {
				t.Fatalf("signal termination swallowed: %v", c.waitErr)
			}
			status, ok := exit.Sys().(syscall.WaitStatus)
			if !ok || !status.Signaled() || status.Signal() != sig {
				t.Fatalf("signal termination status: %v", c.waitErr)
			}
		}
	}
	first, second := start(false), start(false)
	canonical, _ := filepath.EvalSymlinks(data)
	if first.name == second.name || len(registryRecords(t, canonical)) != 2 {
		t.Fatal("two clients did not get independent immutable records")
	}
	secondPath := filepath.Join(canonical+".mcp/servers", second.name)
	before, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	finish(first, 0)
	for _, sig := range []syscall.Signal{0, syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL} {
		c := start(sig == 0) // A persistent shell wrapper must remain just a launcher.
		finish(c, sig)
		records := registryRecords(t, canonical)
		_, remains := records[c.name]
		if remains != (sig == syscall.SIGKILL) {
			t.Fatalf("record lifecycle after %v: %+v", sig, records)
		}
	}
	after, err := os.ReadFile(secondPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("another client's record changed: %q %v", after, err)
	}
	select {
	case <-second.done:
		t.Fatal("another client's server was terminated")
	default:
	}
	finish(second, 0)
	if len(registryRecords(t, canonical)) != 1 {
		t.Fatal("only the SIGKILL crash record should remain")
	}
}
