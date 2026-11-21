package migrate

import (
	"fmt"
	"maps"
	"math"
	"strconv"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
)

// taskStatuses maps every v1 task status (the real file holds todo, active
// and done; the v1 app also let the owner name blocked, open and custom
// statuses) to a v2 workStatus. A value not listed maps to todo with a
// warning.
var taskStatuses = map[string]string{
	"":            "todo",
	"todo":        "todo",
	"open":        "todo",
	"new":         "todo",
	"backlog":     "todo",
	"later":       "todo",
	"someday":     "todo",
	"active":      "active",
	"doing":       "active",
	"wip":         "active",
	"started":     "active",
	"working":     "active",
	"running":     "active",
	"in_progress": "active",
	"in-progress": "active",
	"in progress": "active",
	"blocked":     "blocked",
	"waiting":     "blocked",
	"hold":        "blocked",
	"on_hold":     "blocked",
	"on-hold":     "blocked",
	"stuck":       "blocked",
	"done":        "done",
	"closed":      "done",
	"complete":    "done",
	"completed":   "done",
	"finished":    "done",
	"cancelled":   "done",
	"canceled":    "done",
}

// projectStatuses maps a v1 project status to whether the root task is
// archived, and its workStatus. Every project in the real file is open.
var projectStatuses = map[string]struct {
	archived bool
	work     string
}{
	"":         {false, "todo"},
	"open":     {false, "todo"},
	"active":   {false, "active"},
	"closed":   {true, "done"},
	"done":     {true, "done"},
	"archived": {true, "todo"},
	"inactive": {true, "todo"},
}

// priorities maps a v1 priority (its value or one of its aliases, case
// aside) to the v2 value. v1's P2 and P4 are kept as they are: v2's enum
// has P2 and no P4, and the store holds any value. "" is not set.
var priorities = map[string]string{
	"p0": "P0", "urgent": "P0",
	"p1": "P1", "high": "P1",
	"p2": "P2",
	"p3": "P3", "medium": "P3",
	"p4": "P4",
	"p5": "P5", "low": "P5",
	"": "", "none": "", "normal": "", "not set": "", "unset": "", "-": "",
}

// priority maps a v1 priority and reports whether it was known.
func priority(v string) (string, bool) {
	p, ok := priorities[strings.ToLower(strings.TrimSpace(v))]
	return p, ok
}

// text reads a string value; numbers are formatted.
func text(m map[string]any, key string) string {
	switch v := m[key].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	}
	return ""
}

func boolean(m map[string]any, key string) bool {
	switch v := m[key].(type) {
	case bool:
		return v
	case float64:
		return v != 0
	case string:
		return v == "true" || v == "1"
	}
	return false
}

func integer(m map[string]any, key string) int64 {
	switch v := m[key].(type) {
	case float64:
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			return int64(v)
		}
	case string:
		n, _ := strconv.ParseInt(v, 10, 64)
		return n
	}
	return 0
}

// msTime formats a v1 Unix-millisecond time as a v2 time field ("" when unset).
func msTime(m map[string]any, key string) string {
	ms := integer(m, key)
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(data.TimeFormat)
}

// extra returns the keys of m not in known, without empty values: what the
// mapping did not place, kept on the record as legacyExtra so nothing is lost.
func extra(m map[string]any, known ...string) map[string]any {
	out := maps.Clone(m)
	for _, k := range known {
		delete(out, k)
	}
	for k, v := range out {
		if empty(v) {
			delete(out, k)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func empty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case bool:
		return !x
	case float64:
		return x == 0
	case map[string]any:
		for _, e := range x {
			if !empty(e) {
				return false
			}
		}
		return true
	case []any:
		return len(x) == 0
	}
	return false
}

// setIf sets a field when the value is not empty.
func setIf(fields map[string]any, key string, v any) {
	if !empty(v) {
		fields[key] = v
	}
}

// firstLine is a note's title when v1 kept none: v1 showed the first line.
func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	line = strings.TrimSpace(line)
	if r := []rune(line); len(r) > 80 {
		line = string(r[:80])
	}
	return line
}

var weekdays = map[string]string{"sun": "0", "mon": "1", "tue": "2", "wed": "3", "thu": "4", "fri": "5", "sat": "6"}

// reminderCron derives a cron expression from v1 reminder rules when there is
// exactly one live rule with a time; otherwise the rules alone describe it.
func reminderCron(rules []any) string {
	var live []map[string]any
	for _, r := range rules {
		if m, ok := r.(map[string]any); ok && !boolean(m, "archived") {
			live = append(live, m)
		}
	}
	if len(live) != 1 {
		return ""
	}
	hh, mm, ok := strings.Cut(text(live[0], "time"), ":")
	h, errH := strconv.Atoi(hh)
	mi, errM := strconv.Atoi(mm)
	if !ok || errH != nil || errM != nil || h < 0 || h > 23 || mi < 0 || mi > 59 {
		return ""
	}
	days, _ := live[0]["days"].([]any)
	var nums []string
	seen := map[string]bool{}
	for _, d := range days {
		s, _ := d.(string)
		n, ok := weekdays[strings.ToLower(s)]
		if !ok {
			return ""
		}
		if !seen[n] {
			seen[n] = true
			nums = append(nums, n)
		}
	}
	if len(nums) == 0 {
		return ""
	}
	dow := strings.Join(nums, ",")
	if len(nums) == 7 {
		dow = "*"
	}
	return fmt.Sprintf("%d %d * * %s", mi, h, dow)
}
