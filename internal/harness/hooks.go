package harness

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Per-launch hooks beyond a single settings word (HookArgs): a command-line
// hook per event, with the trust hash the harness demands, and files mtm
// writes for the launch. Both are data on the harness row.

// HookSpec describes hooks given to a harness one event at a time. Command
// is the shell line every event runs ({mtm} is the quoted binary path).
// Args is the argv for one event, with {event}, {command_toml} (Command as a
// TOML basic string) and {timeout} filled in.
type HookSpec struct {
	Command string      `json:"command"`
	Events  []HookEvent `json:"events"`
	Args    []string    `json:"args"`
	// Trust, when set, adds the words that mark each hook trusted: the
	// harness keeps a hook untrusted until its hash is stated.
	Trust *HookTrust `json:"trust,omitempty"`
}

// HookEvent is one event a harness hook runs on. Name is the harness's
// config name (SessionStart), Key its lower-case state-key label
// (session_start), Timeout in seconds.
type HookEvent struct {
	Name    string `json:"name"`
	Key     string `json:"key"`
	Timeout int    `json:"timeout"`
}

// HookTrust says how a hook's trust is stated. StateKey is the harness's key
// for the hook ({key} is the event's Key); Args is the argv word group, with
// {state} as a TOML inline table of key = {trusted_hash = "sha256:..."}.
// The hash is the SHA-256 of the sorted-key compact JSON of the normalized
// hook identity {event_name, hooks:[{type, command, timeout, async}]}, as
// Codex computes it (config/src/fingerprint.rs, hooks/src/engine/discovery.rs).
type HookTrust struct {
	StateKey string   `json:"stateKey"`
	Args     []string `json:"args"`
}

// HookFile is a file a launch carries. Source names an embedded file
// (hookfiles/); Args is the argv word group with {path} the written file;
// SkipIfExists lists files (a leading ~/ is the home directory) whose
// presence means the harness reports already, so the launch adds none.
type HookFile struct {
	Source       string   `json:"source"`
	Args         []string `json:"args"`
	SkipIfExists []string `json:"skipIfExists,omitempty"`
}

//go:embed hookfiles/omp-agent-state.ts
var ompAgentState string

var hookFileSources = map[string]string{"omp-agent-state.ts": ompAgentState}

// HookCommand is the command a hook runs for the mtm binary at path mtm, as
// argv: the small mtm-hook binary beside it when there is one (its first exec
// is quick; mtm's own is slow enough to overrun a hook's budget), else
// mtm agent hook. The hook's own words (harness, flags) follow.
func HookCommand(mtm string) []string {
	sib := filepath.Join(filepath.Dir(mtm), "mtm-hook")
	if st, err := os.Stat(sib); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
		return []string{sib}
	}
	return []string{mtm, "agent", "hook"}
}

// expand fills the placeholders in a harness word for the mtm binary at mtm:
//
//	{mtm}            the shell-quoted path
//	{mtm_json}       {mtm} escaped to sit inside a JSON string
//	{mtm_hook}       HookCommand as a shell command
//	{mtm_hook_json}  {mtm_hook} escaped to sit inside a JSON string
//	{mtm_hook_argv}  HookCommand as a JSON array
func expand(word, mtm string) string {
	if !strings.Contains(word, "{mtm") {
		return word
	}
	argv := HookCommand(mtm)
	hook := strings.Join(append([]string{quoteShell(argv[0])}, argv[1:]...), " ") // the rest are literal words
	arr, _ := json.Marshal(argv)
	return strings.NewReplacer(
		"{mtm_json}", jsonInner(quoteShell(mtm)), "{mtm_hook_json}", jsonInner(hook), "{mtm_hook_argv}", string(arr),
		"{mtm_hook}", hook, "{mtm}", quoteShell(mtm),
	).Replace(word)
}

// HookFileContent is the source of a hook file with the placeholders of
// expand filled in for the mtm binary at mtm.
func HookFileContent(source, mtm string) (string, error) {
	body, ok := hookFileSources[source]
	if !ok {
		return "", fmt.Errorf("no hook file %q", source)
	}
	return expand(body, mtm), nil
}

func jsonInner(s string) string {
	enc, _ := json.Marshal(s)
	return string(enc[1 : len(enc)-1])
}

func quoteShell(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func tomlString(s string) string {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	_ = e.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

// HookHash is the trust hash of one command hook.
func HookHash(eventKey, command string, timeout int) string {
	id := map[string]any{
		"event_name": eventKey,
		"hooks": []any{map[string]any{
			"type": "command", "command": command, "timeout": timeout, "async": false,
		}},
	}
	var b bytes.Buffer
	e := json.NewEncoder(&b) // map keys marshal sorted
	e.SetEscapeHTML(false)
	_ = e.Encode(id)
	sum := sha256.Sum256(bytes.TrimSuffix(b.Bytes(), []byte("\n")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// EventFlags are the words for h.Hooks with mtm filled in; none when h has
// no Hooks or mtm is empty.
func (h Harness) EventFlags(mtm string) []string {
	sp := h.Hooks
	if mtm == "" || sp == nil || len(sp.Events) == 0 {
		return nil
	}
	command := expand(sp.Command, mtm)
	var out, state []string
	for _, ev := range sp.Events {
		r := strings.NewReplacer("{event}", ev.Name, "{command_toml}", tomlString(command), "{timeout}", strconv.Itoa(ev.Timeout))
		for _, a := range sp.Args {
			out = append(out, r.Replace(a))
		}
		if sp.Trust != nil {
			key := strings.ReplaceAll(sp.Trust.StateKey, "{key}", ev.Key)
			state = append(state, tomlString(key)+"={trusted_hash="+tomlString(HookHash(ev.Key, command, ev.Timeout))+"}")
		}
	}
	if sp.Trust != nil {
		table := "{" + strings.Join(state, ",") + "}"
		for _, a := range sp.Trust.Args {
			out = append(out, strings.ReplaceAll(a, "{state}", table))
		}
	}
	return out
}
