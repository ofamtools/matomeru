package schedules

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// When a schedule is due is one of three kinds of timing, each a few
// fields of its Def (so a schedules.json row and a stored row write them
// the same way):
//
//   - an interval (interval: "30m"): every so often from the last run;
//   - days and times (days: ["mon", "fri"], at: ["09:00", "17:30"]): at
//     each time on each day, in the local time zone; times alone are every
//     day, days alone are at 09:00;
//   - a cron line (cron: "0 9 * * 1-5"): minute hour day-of-month month
//     day-of-week, with *, lists, ranges and steps.
//
// start and end bound any of them (a date, 2026-11-01, or an RFC 3339
// time; a date end is the end of that day). The one-line form a person
// types is When's text (ParseWhen, WhenText): "every 30m", "daily 09:00",
// "mon,wed,fri 09:00,17:30", "weekdays 8:30", "cron 0 9 * * 1-5".

// Catch-up values: what a schedule does about the occurrences it missed
// while no mtm ran (more than MissGrace late when it is next checked).
const (
	CatchUpNone = "none" // skip them; the next one is the next on time
	CatchUpOnce = "once" // run once now for all of them
)

// CatchUpValues are the values of catch_up and of the schedules.catch-up
// setting.
var CatchUpValues = []string{CatchUpNone, CatchUpOnce}

// MissGrace is how late an occurrence may be checked and still count as
// on time: the TUI checks every 15s, so anything later was missed.
const MissGrace = 5 * time.Minute

// dayNames are the days in time.Weekday order.
var dayNames = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// dayGroups are the words that stand for several days.
var dayGroups = map[string][]string{
	"daily":    dayNames,
	"everyday": dayNames,
	"weekdays": {"mon", "tue", "wed", "thu", "fri"},
	"weekends": {"sat", "sun"},
}

// When is a schedule's timing, as Def holds it.
type When struct {
	Interval Duration
	Days     []string
	At       []string
	Cron     string
}

// Zero reports no timing at all: the schedule is never due on its own.
func (w When) Zero() bool { return w.Interval == 0 && !w.Calendar() }

// Calendar reports timing by the clock (days, times or cron), not an interval.
func (w When) Calendar() bool { return w.Cron != "" || len(w.Days) > 0 || len(w.At) > 0 }

// Check reports what makes the timing unusable.
func (w When) Check() error {
	switch {
	case w.Interval != 0 && w.Calendar():
		return errors.New("give an interval or days/at/cron, not both")
	case w.Cron != "" && (len(w.Days) > 0 || len(w.At) > 0):
		return errors.New("give cron or days/at, not both")
	case w.Interval != 0 && w.Interval < Duration(time.Minute):
		return errors.New("interval is at least 1m")
	}
	if _, err := normalDays(w.Days); err != nil {
		return err
	}
	for _, at := range w.At {
		if _, _, err := clockTime(at); err != nil {
			return err
		}
	}
	if w.Cron != "" {
		if _, err := parseCron(w.Cron); err != nil {
			return err
		}
	}
	return nil
}

// ParseWhen reads the one-line form: "every 30m" (or "30m"), "daily
// 09:00", "mon,wed 09:00,17:30", "weekdays", "09:00" (every day), "cron
// 0 9 * * 1-5"; "" or "never" is no timing.
func ParseWhen(s string) (When, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" || s == "never" {
		return When{}, nil
	}
	if rest, ok := strings.CutPrefix(s, "cron "); ok {
		w := When{Cron: strings.Join(strings.Fields(rest), " ")}
		return w, w.Check()
	}
	if rest, ok := strings.CutPrefix(s, "every "); ok {
		s = strings.TrimSpace(rest)
	}
	if d, err := time.ParseDuration(s); err == nil {
		w := When{Interval: Duration(d)}
		return w, w.Check()
	}
	var w When
	for _, word := range strings.Fields(strings.ReplaceAll(s, ", ", ",")) {
		for _, part := range strings.Split(word, ",") {
			if part == "" || part == "at" || part == "on" {
				continue
			}
			if _, _, err := clockTime(part); err == nil {
				w.At = append(w.At, part)
				continue
			}
			if _, err := normalDays([]string{part}); err == nil {
				w.Days = append(w.Days, part)
				continue
			}
			return When{}, fmt.Errorf("%q is not a day, a time (09:00, 5:30pm) or an interval (30m); write e.g. \"every 30m\", \"mon,fri 09:00\" or \"cron 0 9 * * 1-5\"", part)
		}
	}
	if err := w.Check(); err != nil {
		return When{}, err
	}
	days, _ := normalDays(w.Days)
	w.Days = days
	w.At = normalTimes(w.At)
	return w, nil
}

// Text is the one-line form ParseWhen reads back: "every 30m", "daily
// 09:00", "mon,fri 09:00", "cron 0 9 * * 1-5", or "never".
func (w When) Text() string {
	switch {
	case w.Cron != "":
		return "cron " + w.Cron
	case w.Interval != 0:
		return "every " + Span(time.Duration(w.Interval))
	case !w.Calendar():
		return "never"
	}
	days, _ := normalDays(w.Days)
	at := normalTimes(w.At)
	if len(at) == 0 {
		at = []string{"09:00"}
	}
	word := strings.Join(days, ",")
	switch {
	case len(days) == 0 || len(days) == 7:
		word = "daily"
	case slices.Equal(days, dayGroups["weekdays"]):
		word = "weekdays"
	case slices.Equal(days, []string{"sun", "sat"}):
		word = "weekends"
	}
	return word + " " + strings.Join(at, ",")
}

// Span is a duration without its zero tail: 5m, 1h, 1h30m.
func Span(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// normalDays are the days as short names in week order, groups expanded.
func normalDays(in []string) ([]string, error) {
	set := map[string]bool{}
	for _, d := range in {
		d = strings.ToLower(strings.TrimSpace(d))
		if group, ok := dayGroups[d]; ok {
			for _, g := range group {
				set[g] = true
			}
			continue
		}
		i := slices.IndexFunc(dayNames, func(n string) bool { return len(d) >= 3 && strings.HasPrefix(d, n) && strings.HasPrefix(fullDay(n), d) })
		if i < 0 {
			return nil, fmt.Errorf("%q is not a day: use mon … sun, weekdays, weekends or daily", d)
		}
		set[dayNames[i]] = true
	}
	var out []string
	for _, n := range dayNames {
		if set[n] {
			out = append(out, n)
		}
	}
	return out, nil
}

func fullDay(short string) string {
	return strings.ToLower(time.Weekday(slices.Index(dayNames, short)).String())
}

// clockTime reads 09:00, 9:00, 17:30, 9am, 5:30pm.
func clockTime(s string) (int, int, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, layout := range []string{"15:04", "3:04pm", "3pm"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Hour(), t.Minute(), nil
		}
	}
	return 0, 0, fmt.Errorf("%q is not a time of day: write 09:00, 17:30 or 5:30pm", s)
}

// normalTimes are the times as HH:MM, sorted, each once.
func normalTimes(in []string) []string {
	var out []string
	for _, at := range in {
		h, m, err := clockTime(at)
		if err != nil {
			continue
		}
		out = append(out, fmt.Sprintf("%02d:%02d", h, m))
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// ParseBound reads a start or end: a date (local midnight; an end is the
// end of that day) or an RFC 3339 time. "" is no bound (the zero time).
func ParseBound(s string, end bool, loc *time.Location) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, loc); err == nil {
		if end {
			return t.AddDate(0, 0, 1).Add(-time.Millisecond), nil
		}
		return t, nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04", "2006-01-02T15:04"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("%q is not a date: write 2026-11-01 or 2026-11-01T09:00:00Z", s)
}

// nextAfter is the first time strictly after t that the calendar timing
// names, in loc; false when there is none (a cron that never matches).
func (w When) nextAfter(t time.Time, loc *time.Location) (time.Time, bool) {
	t = t.In(loc)
	if w.Cron != "" {
		c, err := parseCron(w.Cron)
		if err != nil {
			return time.Time{}, false
		}
		return c.next(t)
	}
	days, _ := normalDays(w.Days)
	at := normalTimes(w.At)
	if len(at) == 0 {
		at = []string{"09:00"}
	}
	for i := 0; i <= 8; i++ {
		day := time.Date(t.Year(), t.Month(), t.Day()+i, 0, 0, 0, 0, loc)
		if len(days) > 0 && !slices.Contains(days, dayNames[day.Weekday()]) {
			continue
		}
		for _, a := range at {
			h, m, _ := clockTime(a)
			c := time.Date(day.Year(), day.Month(), day.Day(), h, m, 0, 0, loc)
			if c.After(t) {
				return c, true
			}
		}
	}
	return time.Time{}, false
}

// cron is a parsed five-field cron line.
type cron struct {
	minute, hour, dom, month, dow []bool
	domAny, dowAny                bool
}

var cronFields = []struct {
	name     string
	min, max int
	names    []string
}{
	{"minute", 0, 59, nil},
	{"hour", 0, 23, nil},
	{"day of month", 1, 31, nil},
	{"month", 1, 12, []string{"", "jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"}},
	{"day of week", 0, 7, dayNames},
}

func parseCron(s string) (cron, error) {
	f := strings.Fields(s)
	if len(f) != 5 {
		return cron{}, fmt.Errorf("cron %q: five fields: minute hour day-of-month month day-of-week", s)
	}
	sets := make([][]bool, 5)
	for i, spec := range cronFields {
		set, err := cronField(strings.ToLower(f[i]), spec.min, spec.max, spec.names)
		if err != nil {
			return cron{}, fmt.Errorf("cron %q: %s: %w", s, spec.name, err)
		}
		sets[i] = set
	}
	if sets[4][7] {
		sets[4][0] = true
	}
	return cron{minute: sets[0], hour: sets[1], dom: sets[2], month: sets[3], dow: sets[4],
		domAny: f[2] == "*", dowAny: f[4] == "*"}, nil
}

func cronField(s string, lo, hi int, names []string) ([]bool, error) {
	set := make([]bool, hi+1)
	num := func(v string) (int, error) {
		if i := slices.Index(names, v); i >= 0 && v != "" {
			return i, nil
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < lo || n > hi {
			return 0, fmt.Errorf("%q is not %d-%d", v, lo, hi)
		}
		return n, nil
	}
	for _, part := range strings.Split(s, ",") {
		rng, step := part, 1
		if r, st, ok := strings.Cut(part, "/"); ok {
			n, err := strconv.Atoi(st)
			if err != nil || n < 1 {
				return nil, fmt.Errorf("step %q", st)
			}
			rng, step = r, n
		}
		from, to := lo, hi
		if rng != "*" {
			a, b, isRange := strings.Cut(rng, "-")
			var err error
			if from, err = num(a); err != nil {
				return nil, err
			}
			to = from
			if isRange {
				if to, err = num(b); err != nil {
					return nil, err
				}
			} else if step > 1 {
				to = hi
			}
			if to < from {
				return nil, fmt.Errorf("range %q runs backwards", rng)
			}
		}
		for v := from; v <= to; v += step {
			set[v] = true
		}
	}
	return set, nil
}

func (c cron) dayMatches(t time.Time) bool {
	dom, dow := c.dom[t.Day()], c.dow[int(t.Weekday())]
	switch {
	case c.domAny && c.dowAny:
		return true
	case c.domAny:
		return dow
	case c.dowAny:
		return dom
	}
	return dom || dow // cron's rule: either restricted field matches
}

// next is the first minute after t that the line matches, within five
// years.
func (c cron) next(t time.Time) (time.Time, bool) {
	loc := t.Location()
	t = t.Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(5, 0, 0)
	for t.Before(limit) {
		switch {
		case !c.month[int(t.Month())]:
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, loc)
		case !c.dayMatches(t):
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, loc)
		case !c.hour[t.Hour()]:
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, loc)
		case !c.minute[t.Minute()]:
			t = t.Add(time.Minute)
		default:
			return t, true
		}
	}
	return time.Time{}, false
}
