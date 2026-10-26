package setup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Hook files (Claude Code's settings.json, Codex's hooks.json) share one
// shape: {"hooks": {EVENT: [{"matcher": …, "hooks": [{"type": "command",
// "command": …, "timeout": …}]}]}}. mtm edits only its own commands (those
// with hookMark) and keeps every other key in its place, so the file reads
// as before apart from them.

// object is a JSON object with its keys in their order.
type object struct {
	keys []string
	vals map[string]json.RawMessage
}

func parseObject(data []byte) (*object, error) {
	o := &object{vals: map[string]json.RawMessage{}}
	if len(bytes.TrimSpace(data)) == 0 {
		return o, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, fmt.Errorf("not a JSON object")
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := tok.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		o.set(key, raw)
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return o, nil
}

func (o *object) set(key string, raw json.RawMessage) {
	if _, ok := o.vals[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = raw
}

func (o *object) del(key string) {
	if _, ok := o.vals[key]; !ok {
		return
	}
	delete(o.vals, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

// render is the object as compact JSON.
func (o *object) render() json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(plain(k))
		b.WriteByte(':')
		b.Write(o.vals[k])
	}
	b.WriteByte('}')
	return b.Bytes()
}

// plain is v as JSON without HTML escaping (a command's && stays &&).
func plain(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return bytes.TrimSpace(b.Bytes())
}

func array(items []json.RawMessage) json.RawMessage {
	parts := make([][]byte, len(items))
	for i, it := range items {
		parts[i] = it
	}
	return json.RawMessage("[" + string(bytes.Join(parts, []byte(","))) + "]")
}

// groupHooks is one group and its list of hooks.
func groupHooks(group json.RawMessage) (*object, []json.RawMessage, error) {
	g, err := parseObject(group)
	if err != nil {
		return nil, nil, err
	}
	var hooks []json.RawMessage
	if raw, ok := g.vals["hooks"]; ok {
		if err := json.Unmarshal(raw, &hooks); err != nil {
			return nil, nil, fmt.Errorf("a hook group's hooks is not a list: %w", err)
		}
	}
	return g, hooks, nil
}

func commandOf(hook json.RawMessage) string {
	var h struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(hook, &h)
	return h.Command
}

// hooksOf is the document and its hooks object.
func hooksOf(doc []byte) (*object, *object, error) {
	root, err := parseObject(doc)
	if err != nil {
		return nil, nil, err
	}
	hooks := &object{vals: map[string]json.RawMessage{}}
	if raw, ok := root.vals["hooks"]; ok {
		if hooks, err = parseObject(raw); err != nil {
			return nil, nil, fmt.Errorf("hooks is not an object: %w", err)
		}
	}
	return root, hooks, nil
}

// ownedHooks reports whether the document has mtm's hooks, and whether
// they are exactly command on every capture event and nowhere else.
func ownedHooks(doc []byte, command string) (owned, current bool) {
	_, hooks, err := hooksOf(doc)
	if err != nil {
		return false, false
	}
	current = true
	found := map[string]bool{}
	for _, event := range hooks.keys {
		var groups []json.RawMessage
		if json.Unmarshal(hooks.vals[event], &groups) != nil {
			continue
		}
		for _, group := range groups {
			_, list, err := groupHooks(group)
			if err != nil {
				continue
			}
			for _, h := range list {
				c := commandOf(h)
				if !strings.Contains(c, hookMark) {
					continue
				}
				owned = true
				if c != command || found[event] {
					current = false
				}
				found[event] = true
			}
		}
	}
	for _, event := range hookEvents {
		current = current && found[event]
	}
	return owned, owned && current
}

// editHooks removes mtm's commands from every event and, with install,
// adds one group per capture event running harness's hook command.
func editHooks(doc []byte, o Options, harness string, install bool) ([]byte, error) {
	root, hooks, err := hooksOf(doc)
	if err != nil {
		return nil, err
	}
	removed := false
	for _, event := range append([]string(nil), hooks.keys...) {
		var groups []json.RawMessage
		if err := json.Unmarshal(hooks.vals[event], &groups); err != nil {
			return nil, fmt.Errorf("hooks.%s is not a list: %w", event, err)
		}
		var kept []json.RawMessage
		changed := false
		for _, group := range groups {
			g, list, err := groupHooks(group)
			if err != nil {
				return nil, fmt.Errorf("hooks.%s: %w", event, err)
			}
			var others []json.RawMessage
			for _, h := range list {
				if !strings.Contains(commandOf(h), hookMark) {
					others = append(others, h)
				}
			}
			switch {
			case len(others) == len(list):
				kept = append(kept, group)
			case len(others) > 0:
				g.set("hooks", array(others))
				kept = append(kept, g.render())
				changed = true
			default:
				changed = true
			}
		}
		removed = removed || changed
		switch {
		case !changed:
		case len(kept) == 0:
			hooks.del(event)
		default:
			hooks.set(event, array(kept))
		}
	}
	if install {
		command := o.HookCommand(harness)
		for _, event := range hookEvents {
			group := plain(map[string]any{"hooks": []map[string]any{{"type": "command", "command": command, "timeout": 10}}})
			var groups []json.RawMessage
			if raw, ok := hooks.vals[event]; ok {
				_ = json.Unmarshal(raw, &groups)
			}
			hooks.set(event, array(append(groups, group)))
		}
	}
	switch {
	case len(hooks.keys) > 0:
		root.set("hooks", hooks.render())
	case removed:
		root.del("hooks") // emptied by removing mtm's own hooks
	}
	var out bytes.Buffer
	if err := json.Indent(&out, root.render(), "", "  "); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}
