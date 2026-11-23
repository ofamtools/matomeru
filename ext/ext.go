// Package ext finds the o-* tools that extend Matomeru, probes their health
// and keeps one extension record per tool, and runs extension commands
// through runs.Runner so each appears as a run.
//
// Probe convention. Each probe is a read-only invocation with a timeout:
//
//  1. `TOOL --version`: a tool that runs and exits (with any status) is
//     healthy, and the first line of its stdout, when it exits 0, is its
//     version. A tool that cannot be executed or does not exit in time is
//     offline.
//  2. `TOOL capabilities --json`: a tool may describe itself by printing
//     {"summary":"...","version":"...","capabilities":[...],"commands":[...]}
//     and exiting 0. Any field may be left out.
//  3. Otherwise `TOOL --help` is parsed: the indented lines under a heading
//     that ends in "commands:" (any case) name one command each, by their
//     first word.
package ext

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
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/runs"
	"ofam.dev/ochazuke/core"
)

// Health states written to an extension's runStatus.
const (
	StatusHealthy = "healthy"
	StatusOffline = "offline"
)

// DefaultTools are the o-* tools Discover looks for, including side-by-side v2
// binaries (tools not found on PATH produce no record).
var DefaultTools = []string{
	"o-csv", "o-csv2",
	"o-git",
	"o-lane", "o-lane2",
	"o-json", "o-json2",
	"o-links",
	"o-md", "o-md2",
	"o-cmds", "o-cmds2",
	"o-questions", "o-questions2",
	"o-df", "o-df2",
}

// Defaults for Options left zero.
const (
	DefaultMaxProcesses = 4
	DefaultProbeTimeout = 2 * time.Second
	maxProbeOutput      = 64 << 10
)

// Options tunes a Manager. Zero values take the defaults.
type Options struct {
	// Tools are the tool names to discover; nil means DefaultTools.
	Tools []string
	// MaxProcesses caps the probe and extension processes running at once.
	MaxProcesses int
	// ProbeTimeout bounds each probe invocation.
	ProbeTimeout time.Duration
	// Actor is who the extension and run records are written as.
	Actor core.Actor
}

// Manager discovers extensions and runs their commands.
type Manager struct {
	opts Options
	sem  chan struct{}
}

// New returns a Manager.
func New(opts Options) *Manager {
	if opts.Tools == nil {
		opts.Tools = DefaultTools
	}
	if opts.MaxProcesses <= 0 {
		opts.MaxProcesses = DefaultMaxProcesses
	}
	if opts.ProbeTimeout <= 0 {
		opts.ProbeTimeout = DefaultProbeTimeout
	}
	if opts.Actor.Kind == "" {
		opts.Actor = core.Actor{Kind: "system", ID: "ext"}
	}
	return &Manager{opts: opts, sem: make(chan struct{}, opts.MaxProcesses)}
}

// Result is what Discover found for one tool. Record is nil for a tool that
// was never found and has no record.
type Result struct {
	Name         string
	Binary       string // "" when not found
	Status       string
	Version      string
	Summary      string
	Capabilities []string
	Commands     []string
	Err          error // why the tool is offline, when it is
	Record       *data.Record
	Changed      bool // the record was created or updated
}

// Discover probes every tool, looking in searchDirs and then PATH, and
// upserts one extension record per tool by name. A tool never found gets no
// record; one found before and now gone is marked offline and keeps the rest
// of its record. Nothing is written for a record that would not change.
func (m *Manager) Discover(ctx context.Context, searchDirs []string, store *data.Store) ([]Result, error) {
	results := make([]Result, len(m.opts.Tools))
	var wg sync.WaitGroup
	for i, name := range m.opts.Tools {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = m.probe(ctx, name, searchDirs)
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return results, err
	}
	var errs []error
	for i := range results {
		if err := m.upsert(store, &results[i]); err != nil {
			errs = append(errs, fmt.Errorf("ext: record %s: %w", results[i].Name, err))
		}
	}
	return results, errors.Join(errs...)
}

func (m *Manager) upsert(store *data.Store, res *Result) error {
	existing, err := store.GetByField(data.KindExtension, "name", res.Name)
	if err != nil && !errors.Is(err, data.ErrNotFound) {
		return err
	}
	if existing == nil {
		if res.Binary == "" {
			return nil
		}
		rec, _, err := store.Create(data.CreateInput{Kind: data.KindExtension, Fields: res.fields()}, m.opts.Actor)
		res.Record, res.Changed = rec, err == nil
		return err
	}
	res.Record = existing
	changes := map[string]any{}
	for k, v := range res.fields() {
		if cur, had := existing.Fields[k]; !had || !sameJSON(cur, v) {
			changes[k] = v
		}
	}
	if len(changes) == 0 {
		return nil
	}
	rec, cs, err := store.Patch(data.PatchInput{Ref: existing.Ref(), Fields: changes}, m.opts.Actor)
	if err != nil {
		return err
	}
	res.Record, res.Changed = rec, cs != nil
	return nil
}

// fields are the extension fields a probe result sets. An offline probe
// sets only the health (and the binary, when one was found), so what an
// earlier healthy probe learned is kept.
func (res Result) fields() map[string]any {
	f := map[string]any{"name": res.Name, "runStatus": res.Status}
	if res.Binary != "" {
		f["binary"] = res.Binary
	}
	if res.Status != StatusHealthy {
		return f
	}
	f["capabilities"] = nonNil(res.Capabilities)
	f["commands"] = nonNil(res.Commands)
	if res.Summary != "" {
		f["summary"] = res.Summary
	}
	if res.Version != "" {
		f["version"] = res.Version
	}
	return f
}

func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

func sameJSON(a, b any) bool {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(ja, jb)
}

// find returns the first regular file named name in dirs, then on PATH.
func find(name string, dirs []string) string {
	for _, dir := range dirs {
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path
		}
	}
	if path, err := exec.LookPath(name); err == nil {
		if abs, err := filepath.Abs(path); err == nil {
			return abs
		}
		return path
	}
	return ""
}

func (m *Manager) probe(ctx context.Context, name string, dirs []string) Result {
	res := Result{Name: name, Status: StatusOffline}
	res.Binary = find(name, dirs)
	if res.Binary == "" {
		res.Err = fmt.Errorf("%s not found", name)
		return res
	}
	out, exited, err := m.invoke(ctx, res.Binary, "--version")
	if !exited {
		res.Err = err
		return res
	}
	res.Status = StatusHealthy
	if err == nil {
		res.Version = firstLine(out)
	}
	if out, _, err := m.invoke(ctx, res.Binary, "capabilities", "--json"); err == nil {
		var desc struct {
			Summary      string   `json:"summary"`
			Version      string   `json:"version"`
			Capabilities []string `json:"capabilities"`
			Commands     []string `json:"commands"`
		}
		if json.Unmarshal(out, &desc) == nil {
			res.Summary, res.Capabilities, res.Commands = desc.Summary, desc.Capabilities, desc.Commands
			if desc.Version != "" {
				res.Version = desc.Version
			}
			return res
		}
	}
	if out, exited, _ := m.invoke(ctx, res.Binary, "--help"); exited {
		res.Commands = helpCommands(out)
	}
	return res
}

// invoke runs one probe under the process cap and the probe timeout. exited
// reports that the process ran and exited by itself; err is nil only for a
// zero exit.
func (m *Manager) invoke(ctx context.Context, bin string, args ...string) (out []byte, exited bool, err error) {
	if err := m.acquire(ctx); err != nil {
		return nil, false, err
	}
	defer m.release()
	pctx, cancel := context.WithTimeout(ctx, m.opts.ProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(pctx, bin, args...)
	cmd.Dir = filepath.Dir(bin)
	var stdout limitedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 500 * time.Millisecond
	err = cmd.Run()
	if pctx.Err() != nil {
		return stdout.Bytes(), false, fmt.Errorf("%s %s: no answer in %v", bin, strings.Join(args, " "), m.opts.ProbeTimeout)
	}
	var exitErr *exec.ExitError
	return stdout.Bytes(), err == nil || errors.As(err, &exitErr), err
}

func (m *Manager) acquire(ctx context.Context) error {
	select {
	case m.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) release() { <-m.sem }

func firstLine(out []byte) string {
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(line)
}

// helpCommands reads command names from help text: the first word of each
// indented line under a heading ending in "commands:".
func helpCommands(out []byte) []string {
	var cmds []string
	in := false
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			continue
		case line[0] != ' ' && line[0] != '\t':
			in = strings.HasSuffix(strings.ToLower(trimmed), "commands:")
		case in:
			word, _, _ := strings.Cut(trimmed, " ")
			word = strings.TrimRight(strings.Fields(word)[0], ":,")
			if word != "" && !strings.HasPrefix(word, "-") && !slices.Contains(cmds, word) {
				cmds = append(cmds, word)
			}
		}
	}
	return cmds
}

// limitedBuffer keeps the first maxProbeOutput bytes written to it.
type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := maxProbeOutput - b.Len(); room > 0 {
		b.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// RunCommand runs an extension's binary with args through runner, so it is
// recorded as a run titled "NAME ARGS...". It refuses an unknown or offline
// extension. It counts against MaxProcesses until the run ends, and waits
// for room (or ctx) when the cap is reached.
func (m *Manager) RunCommand(ctx context.Context, extName string, args []string, runner *runs.Runner) (*data.Record, error) {
	ext, err := runner.Store().GetByField(data.KindExtension, "name", extName)
	if errors.Is(err, data.ErrNotFound) {
		return nil, fmt.Errorf("ext: no extension named %q; discover extensions first", extName)
	}
	if err != nil {
		return nil, err
	}
	bin := ext.Text("binary")
	if ext.Text("runStatus") == StatusOffline || bin == "" {
		return nil, fmt.Errorf("ext: extension %s is offline", extName)
	}
	if err := m.acquire(ctx); err != nil {
		return nil, err
	}
	title := strings.TrimSpace(extName + " " + strings.Join(args, " "))
	rec, err := runner.Start(ctx, runs.StartSpec{Title: title, Args: append([]string{bin}, args...), Actor: m.opts.Actor})
	if rec == nil {
		m.release()
		return nil, err
	}
	done := runner.Done(rec.ID)
	go func() {
		<-done
		m.release()
	}()
	return rec, err
}
