package turns

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An activity log follows a session's subagents alone: a background
// agent runs until its notification ends it (by its call, or by its task
// id alone), a foreground one until its result; a failed and a killed
// one end bad; one waiting on its own agents still runs. Lines appended
// later are read from where the last read stopped.
func TestActivityLogFollowsSubagents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s1.jsonl")
	write := func(lines ...string) {
		t.Helper()
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if _, err := f.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
			t.Fatal(err)
		}
	}
	write(
		userPrompt("u1", "2026-10-28T10:00:00Z", "start them", ""),
		agentCall("a1", "2026-10-28T10:00:01Z", "toolu_fg", "Foreground audit", false),
		agentCall("a2", "2026-10-28T10:00:02Z", "toolu_bg", "Background export", true),
		launched("r2", "2026-10-28T10:00:03Z", "toolu_bg", "ab1"),
		agentCall("a3", "2026-10-28T10:00:04Z", "toolu_x", "Failing agent", true),
		launched("r3", "2026-10-28T10:00:05Z", "toolu_x", "ax1"),
		agentCall("a4", "2026-10-28T10:00:06Z", "toolu_k", "Killed agent", true),
		launched("r4", "2026-10-28T10:00:07Z", "toolu_k", "ak1"),
		agentCall("a5", "2026-10-28T10:00:08Z", "toolu_w", "Waiting agent", true),
		launched("r5", "2026-10-28T10:00:09Z", "toolu_w", "aw1"),
		assistant("a6", "2026-10-28T10:00:10Z", "tool_use", toolUse("toolu_bash", "Bash", map[string]any{"command": "ls"})),
		toolResult("r6", "2026-10-28T10:00:11Z", "toolu_bash", "a b", false),
	)
	log := NewActivityLog(path, "s1")
	if _, err := log.Read(); err != nil {
		t.Fatal(err)
	}
	states := func() map[string]string {
		out := map[string]string{}
		for _, a := range log.Activities() {
			out[a.Title] = string(a.State) + "/" + a.Status
		}
		return out
	}
	want := map[string]string{
		"Foreground audit": "running/running", "Background export": "running/running", "Failing agent": "running/running",
		"Killed agent": "running/running", "Waiting agent": "running/running",
	}
	if got := states(); !sameMap(got, want) {
		t.Fatalf("first read: %v", got)
	}
	if acts := log.Activities(); acts[0].Kind != "general-purpose" || acts[0].ID != "toolu_fg" || acts[0].StartedAt.IsZero() {
		t.Fatalf("first activity: %+v", acts[0])
	}
	write(
		toolResult("r1", "2026-10-28T10:01:00Z", "toolu_fg", "The audit is done.\nagentId: af1", false),
		notify("n1", "2026-10-28T10:02:00Z", RealNotification("ab1", "", "completed", `Agent "Background export" finished`, "done")),
		notify("n2", "2026-10-28T10:03:00Z", RealNotification("ax1", "toolu_x", "failed", `Agent "Failing agent" failed: API error`, "")),
		notify("n3", "2026-10-28T10:04:00Z", RealNotification("ak1", "toolu_k", "killed", `Agent "Killed agent" was stopped by Claude`, "")),
		notify("n4", "2026-10-28T10:05:00Z", RealNotification("aw1", "toolu_w", "completed", `Agent "Waiting agent" is waiting on its own background work`, "")),
	)
	if changed, err := log.Read(); err != nil || !changed {
		t.Fatalf("second read: %v %v", changed, err)
	}
	want = map[string]string{
		"Foreground audit": "done/completed", "Background export": "done/completed", "Failing agent": "failed/failed",
		"Killed agent": "stopped/killed", "Waiting agent": "running/waiting on its own work",
	}
	if got := states(); !sameMap(got, want) {
		t.Fatalf("second read: %v", got)
	}
	for _, a := range log.Activities() {
		if a.Title == "Failing agent" && (a.EndedAt.IsZero() || a.Summary == "") {
			t.Fatalf("failed agent: %+v", a)
		}
	}
}

func sameMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// A background agent that stopped while its own background work runs
// gets Claude Code's "waiting on its own background work" notification,
// and its meta is written finished with it (the owner's coordinator,
// 2026-10-30): the notification's word stands, so it still runs, until a
// later notification of the same task ends it.
func TestActivityWaitingOnItsOwnWorkOutlivesAFinishedMeta(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s1.jsonl")
	lines := []string{
		userPrompt("u1", "2026-10-30T20:56:00Z", "keep going", ""),
		agentCall("a1", "2026-10-30T20:56:01Z", "toolu_w", "o-jj: rough edges", true),
		launched("r1", "2026-10-30T20:56:02Z", "toolu_w", "aw1"),
		notify("n1", "2026-10-30T21:49:47Z", RealNotification("aw1", "toolu_w", "completed", `Agent "o-jj: rough edges" is waiting on its own background work`, "")),
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	subs := SubagentDir(path)
	if err := os.MkdirAll(subs, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := `{"agentType":"ofam-lane","description":"o-jj: rough edges","toolUseId":"toolu_w","spawnDepth":1,"requestShape":"background","finished":true}`
	if err := os.WriteFile(filepath.Join(subs, "agent-aw1.meta.json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	log := NewActivityLog(path, "s1")
	if _, err := log.Read(); err != nil {
		t.Fatal(err)
	}
	acts := log.Activities()
	if len(acts) != 1 || !acts[0].Running() || acts[0].Status != "waiting on its own work" {
		t.Fatalf("waiting agent: %+v", acts)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(notify("n2", "2026-10-30T22:10:00Z", RealNotification("aw1", "toolu_w", "completed", `Agent "o-jj: rough edges" finished`, "done")) + "\n")
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := log.Read(); err != nil {
		t.Fatal(err)
	}
	if acts := log.Activities(); acts[0].Running() || acts[0].Status != "completed" {
		t.Fatalf("after its report: %+v", acts[0])
	}
}
