package daemon

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

// DefaultSchedulerInterval is how often the daemon evaluates due schedules.
const DefaultSchedulerInterval = 15 * time.Second

var daemonActor = core.Actor{Kind: "system", ID: "mtm-daemon", Label: "daemon"}

// Notifier delivers a reminder notification for scheduleKind="remind".
type Notifier interface {
	Notify(ctx context.Context, title, body string) error
}

// NotifierFunc adapts a function to Notifier.
type NotifierFunc func(ctx context.Context, title, body string) error

// Notify implements Notifier.
func (f NotifierFunc) Notify(ctx context.Context, title, body string) error {
	return f(ctx, title, body)
}

// MacOSNotifier sends desktop notifications via osascript on macOS (no-op on other OSes).
type MacOSNotifier struct{}

// Notify implements Notifier.
func (MacOSNotifier) Notify(ctx context.Context, title, body string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	msg := body
	if msg == "" {
		msg = title
	}
	script := fmt.Sprintf(`display notification "%s" with title "%s"`, EscapeAppleScript(msg), EscapeAppleScript(title))
	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("osascript notification: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// EscapeAppleScript escapes a string for inclusion inside AppleScript double quotes.
func EscapeAppleScript(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", "")
	return s
}

// Runner executes a shell command for scheduleKind="run" and "check".
type Runner interface {
	Run(ctx context.Context, command string) (string, error)
}

// RunnerFunc adapts a function to Runner.
type RunnerFunc func(ctx context.Context, command string) (string, error)

// Run implements Runner.
func (f RunnerFunc) Run(ctx context.Context, command string) (string, error) { return f(ctx, command) }

// ShellRunner executes commands with /bin/sh -c.
type ShellRunner struct{}

// Run implements Runner.
func (ShellRunner) Run(ctx context.Context, command string) (string, error) {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// CronExpr is a parsed 5-field cron expression (minute, hour, dom, month, dow).
type CronExpr struct {
	minutes    uint64 // bits 0..59
	hours      uint32 // bits 0..23
	dom        uint32 // bits 1..31
	months     uint16 // bits 1..12
	dow        uint8  // bits 0..6 (0=Sunday)
	domStar    bool
	dowStar    bool
	everyEvery time.Duration // optional @every <duration>
}

// ParseCron parses a standard 5-field cron expression or @hourly/@daily/@weekly/@monthly/@every.
func ParseCron(spec string) (*CronExpr, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, fmt.Errorf("empty cron expression")
	}
	switch strings.ToLower(spec) {
	case "@hourly":
		spec = "0 * * * *"
	case "@daily", "@midnight":
		spec = "0 0 * * *"
	case "@weekly":
		spec = "0 0 * * 0"
	case "@monthly":
		spec = "0 0 1 * *"
	default:
		if rest, ok := strings.CutPrefix(strings.ToLower(spec), "@every "); ok {
			d, err := time.ParseDuration(strings.TrimSpace(rest))
			if err != nil || d <= 0 {
				return nil, fmt.Errorf("invalid @every duration %q", rest)
			}
			return &CronExpr{everyEvery: d}, nil
		}
	}

	fields := strings.Fields(spec)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron expression %q must have 5 fields", spec)
	}
	mins, err := parseCronField(fields[0], 0, 59, false)
	if err != nil {
		return nil, fmt.Errorf("cron minute %q: %w", fields[0], err)
	}
	hrs, err := parseCronField(fields[1], 0, 23, false)
	if err != nil {
		return nil, fmt.Errorf("cron hour %q: %w", fields[1], err)
	}
	dom, err := parseCronField(fields[2], 1, 31, false)
	if err != nil {
		return nil, fmt.Errorf("cron day-of-month %q: %w", fields[2], err)
	}
	mon, err := parseCronField(fields[3], 1, 12, false)
	if err != nil {
		return nil, fmt.Errorf("cron month %q: %w", fields[3], err)
	}
	dow, err := parseCronField(fields[4], 0, 7, true)
	if err != nil {
		return nil, fmt.Errorf("cron day-of-week %q: %w", fields[4], err)
	}
	return &CronExpr{
		minutes: mins,
		hours:   uint32(hrs),
		dom:     uint32(dom),
		months:  uint16(mon),
		dow:     uint8(dow),
		domStar: fields[2] == "*",
		dowStar: fields[4] == "*",
	}, nil
}

func parseCronField(field string, minVal, maxVal int, wrapSunday bool) (uint64, error) {
	var bits uint64
	for part := range strings.SplitSeq(field, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return 0, fmt.Errorf("empty element")
		}
		step := 1
		rangePart := part
		if slash := strings.IndexByte(part, '/'); slash >= 0 {
			rangePart = part[:slash]
			stepStr := part[slash+1:]
			n, err := strconv.Atoi(stepStr)
			if err != nil || n <= 0 {
				return 0, fmt.Errorf("invalid step %q", stepStr)
			}
			step = n
		}
		var start, end int
		switch {
		case rangePart == "*" || rangePart == "":
			start, end = minVal, maxVal
			if wrapSunday && end == 7 {
				end = 6
			}
		case strings.Contains(rangePart, "-"):
			a, b, _ := strings.Cut(rangePart, "-")
			s, err1 := strconv.Atoi(a)
			e, err2 := strconv.Atoi(b)
			if err1 != nil || err2 != nil || s < minVal || e > maxVal || s > e {
				return 0, fmt.Errorf("invalid range %q", rangePart)
			}
			start, end = s, e
		default:
			v, err := strconv.Atoi(rangePart)
			if err != nil || v < minVal || v > maxVal {
				return 0, fmt.Errorf("value %q out of bounds [%d,%d]", rangePart, minVal, maxVal)
			}
			start = v
			if strings.Contains(part, "/") {
				end = maxVal
				if wrapSunday && end == 7 {
					end = 6
				}
			} else {
				end = v
			}
		}
		for v := start; v <= end; v += step {
			bit := v
			if wrapSunday && bit == 7 {
				bit = 0
			}
			bits |= 1 << uint(bit)
		}
	}
	if bits == 0 {
		return 0, fmt.Errorf("matches nothing")
	}
	return bits, nil
}

// Matches reports whether t satisfies the cron expression (evaluated at minute granularity).
func (c *CronExpr) Matches(t time.Time) bool {
	if c == nil {
		return false
	}
	if c.everyEvery > 0 {
		return true
	}
	if (c.minutes&(1<<uint(t.Minute()))) == 0 ||
		(c.hours&(1<<uint(t.Hour()))) == 0 ||
		(c.months&(1<<uint(t.Month()))) == 0 {
		return false
	}
	domMatch := (c.dom & (1 << uint(t.Day()))) != 0
	dowMatch := (c.dow & (1 << uint(t.Weekday()))) != 0
	if !c.domStar && !c.dowStar {
		return domMatch || dowMatch
	}
	return domMatch && dowMatch
}

// Next returns the next time strictly after after that matches the cron expression.
// Returns the zero time if no match exists within 4 years.
func (c *CronExpr) Next(after time.Time) time.Time {
	if c == nil {
		return time.Time{}
	}
	if c.everyEvery > 0 {
		return after.Add(c.everyEvery)
	}
	// Advance to the next whole minute strictly after after.
	t := after.Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(4, 0, 0)
	for !t.After(limit) {
		if (c.months & (1 << uint(t.Month()))) == 0 {
			// Skip to the first of next month at 00:00.
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location())
			continue
		}
		domMatch := (c.dom & (1 << uint(t.Day()))) != 0
		dowMatch := (c.dow & (1 << uint(t.Weekday()))) != 0
		dayOK := domMatch && dowMatch
		if !c.domStar && !c.dowStar {
			dayOK = domMatch || dowMatch
		}
		if !dayOK {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
			continue
		}
		if (c.hours & (1 << uint(t.Hour()))) == 0 {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, t.Location())
			continue
		}
		if (c.minutes & (1 << uint(t.Minute()))) == 0 {
			t = t.Add(time.Minute)
			continue
		}
		return t
	}
	return time.Time{}
}

// Scheduler evaluates due "schedule" records in a *data.Store.
type Scheduler struct {
	store    *data.Store
	notifier Notifier
	runner   Runner
	clock    func() time.Time

	mu      sync.Mutex
	running bool
}

// SchedulerConfig configures a Scheduler.
type SchedulerConfig struct {
	Store    *data.Store
	Notifier Notifier
	Runner   Runner
	Clock    func() time.Time
}

// NewScheduler constructs a Scheduler over cfg.Store.
func NewScheduler(cfg SchedulerConfig) *Scheduler {
	notifier := cfg.Notifier
	if notifier == nil {
		notifier = MacOSNotifier{}
	}
	runner := cfg.Runner
	if runner == nil {
		runner = ShellRunner{}
	}
	clock := cfg.Clock
	if clock == nil {
		clock = time.Now
	}
	return &Scheduler{
		store:    cfg.Store,
		notifier: notifier,
		runner:   runner,
		clock:    clock,
	}
}

// Start runs Tick periodically in a background goroutine until ctx is cancelled
// or the returned stop function is called.
func (s *Scheduler) Start(ctx context.Context, interval time.Duration) func() {
	if s == nil || s.store == nil {
		return func() {}
	}
	if interval <= 0 {
		interval = DefaultSchedulerInterval
	}
	loopCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		// Evaluate once shortly after startup.
		_ = s.Tick(loopCtx)
		for {
			select {
			case <-loopCtx.Done():
				return
			case <-ticker.C:
				_ = s.Tick(loopCtx)
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}
}

// scheduleIsDisabled returns true if the schedule is archived, snoozed, paused, or disabled.
func scheduleIsDisabled(rec data.Record, now time.Time) bool {
	if rec.IsArchived() || rec.IsSnoozed(now) {
		return true
	}
	if rec.Bool("paused") {
		return true
	}
	if _, hasEnabled := rec.Value("enabled"); hasEnabled && !rec.Bool("enabled") {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(rec.Text("status"))) {
	case "paused", "disabled", "stopped":
		return true
	}
	return false
}

// Tick evaluates all active schedules in the store at the current clock time.
// Concurrent calls to Tick are serialized so a schedule never double-fires.
func (s *Scheduler) Tick(ctx context.Context) error {
	if s == nil || s.store == nil {
		return nil
	}
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	now := s.clock()
	res, err := s.store.Query(data.Query{Kind: data.KindSchedule})
	if err != nil {
		return err
	}
	var firstErr error
	for _, rec := range res.Items {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if scheduleIsDisabled(rec, now) {
			continue
		}
		if err := s.evalSchedule(ctx, rec, now); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *Scheduler) evalSchedule(ctx context.Context, rec data.Record, now time.Time) error {
	cronSpec := strings.TrimSpace(rec.Text("cron"))
	var expr *CronExpr
	if cronSpec != "" {
		var err error
		expr, err = ParseCron(cronSpec)
		if err != nil {
			return fmt.Errorf("schedule %s: %w", rec.ID, err)
		}
	}

	nextDue := rec.Time("nextDueAt")
	var dueAt time.Time
	var nextTime time.Time

	switch {
	case !nextDue.IsZero():
		if now.Before(nextDue) {
			return nil
		}
		dueAt = nextDue
		if expr != nil {
			nextTime = expr.Next(now)
		}
	case expr != nil:
		if expr.Matches(now) {
			dueAt = now.Truncate(time.Minute)
			nextTime = expr.Next(dueAt)
		} else {
			// Initialize nextDueAt so future ticks know when it becomes due.
			next := expr.Next(now)
			if !next.IsZero() {
				_, _, _ = s.store.Patch(data.PatchInput{
					Ref:     rec.Ref(),
					Fields:  map[string]any{"nextDueAt": next.UTC().Format(data.TimeFormat)},
					Summary: "initialize nextDueAt",
				}, daemonActor)
			}
			return nil
		}
	default:
		return nil
	}

	// Create schedule.occurrence record.
	occ, _, err := s.store.Create(data.CreateInput{
		Kind: data.KindOccurrence,
		Fields: map[string]any{
			"schedule":      rec.Ref(),
			"dueAt":         dueAt.UTC().Format(data.TimeFormat),
			"outcomeStatus": "pending",
		},
	}, daemonActor)
	if err != nil {
		return err
	}

	kind := rec.Text("scheduleKind")
	if kind == "" {
		kind = "remind"
	}
	title := rec.Text("title")
	command := rec.Text("command")

	outcome := "done"
	switch kind {
	case "remind":
		if err := s.notifier.Notify(ctx, title, fmt.Sprintf("Scheduled reminder (%s)", dueAt.UTC().Format(time.RFC3339))); err != nil {
			outcome = "failed"
		}
	case "run":
		if command == "" {
			outcome = "skipped"
		} else if _, err := s.runner.Run(ctx, command); err != nil {
			outcome = "failed"
		}
	case "check":
		body := ""
		eventKind := "check"
		if command != "" {
			out, rerr := s.runner.Run(ctx, command)
			body = strings.TrimSpace(out)
			if rerr != nil {
				outcome = "failed"
				eventKind = "failure"
				if body == "" {
					body = rerr.Error()
				} else {
					body = body + "\n" + rerr.Error()
				}
			}
		}
		sigFields := map[string]any{
			"title":       title,
			"source":      "schedule",
			"eventKind":   eventKind,
			"author":      "mtm-daemon",
			"container":   rec.ID,
			"body":        body,
			"occurredAt":  now.UTC().Format(data.TimeFormat),
			"triageState": data.TriageUnreviewed,
		}
		if taskRef, ok := rec.RefField("task"); ok {
			sigFields["task"] = taskRef
		}
		if _, _, serr := s.store.Create(data.CreateInput{Kind: data.KindSignal, Fields: sigFields}, daemonActor); serr != nil {
			outcome = "failed"
		}
	default:
		outcome = "skipped"
	}

	_, _, _ = s.store.Patch(data.PatchInput{
		Ref: occ.Ref(),
		Fields: map[string]any{
			"outcomeStatus": outcome,
			"checkedAt":     now.UTC().Format(data.TimeFormat),
		},
		Summary: "record schedule occurrence outcome",
	}, daemonActor)

	var nextVal any
	if !nextTime.IsZero() {
		nextVal = nextTime.UTC().Format(data.TimeFormat)
	}
	_, _, err = s.store.Patch(data.PatchInput{
		Ref:     rec.Ref(),
		Fields:  map[string]any{"nextDueAt": nextVal},
		Summary: "advance nextDueAt",
	}, daemonActor)
	return err
}
