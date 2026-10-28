package main

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// This file spells the key names ochazuke forwards from a live pane (Bubble
// Tea v2's Key.String: the key's text, else its keystroke such as
// "ctrl+shift+/") the way tuios send-keys parses them. The grammar is
// tuios's internal/session/sendkeys.go: parseSendKeys splits on spaces and
// commas (:359), parseKeyToken takes one character as itself (:399), a
// modifier list joined by + (splitKeyMods :522: ctrl, alt, shift, super;
// anything else is an unknown key :563), a named key (namedKeys :94 and
// keyAliases :131: Enter, Tab, Space, Comma, Escape, Backspace, the arrows,
// Home, End, PageUp, PageDown, Insert, Delete, F1 to F12), and any other
// word as its letters (:435). ctrl goes only on a letter or one of
// @ space 2 [ \ ] ^ 6 _ - ? (controlByte :495; anything else is the
// "unsupported ctrl combination" error :484), and super has no encoding in
// a pane (sendKeysBytes :712). A key outside that grammar is not sent.

// paneShifted is the character shift makes of a key on a US layout, which
// is how Bubble Tea names a shifted symbol (its base code) under the kitty
// keyboard protocol: ctrl+shift+/ is ctrl+?.
var paneShifted = map[rune]rune{
	'`': '~', '1': '!', '2': '@', '3': '#', '4': '$', '5': '%', '6': '^',
	'7': '&', '8': '*', '9': '(', '0': ')', '-': '_', '=': '+',
	'[': '{', ']': '}', '\\': '|', ';': ':', '\'': '"', ',': '<', '.': '>', '/': '?',
}

// paneCtrl is ctrl on a character send-keys refuses ctrl on, spelled as the
// character it accepts that sends the same control byte a legacy (xterm,
// VT220) terminal sends for it: ctrl+/ is 0x1f, ctrl+_. A character in
// neither this table nor paneCtrlOK has no control byte and is not sent.
var paneCtrl = map[rune]rune{
	'`': '@', '~': '^', '{': '[', '}': ']', '|': '\\', '/': '_',
	'3': '[', '4': '\\', '5': ']', '7': '_', '8': '?',
}

// paneCtrlOK is every character send-keys takes ctrl on besides a letter
// (controlByte).
const paneCtrlOK = "@ 2[\\]^6_-?"

// paneNamed is a Bubble Tea key name send-keys spells otherwise; a name it
// shares (enter, tab, backspace, up, pgup, delete, f1 to f12) is sent as
// it is. The keypad names Bubble Tea gives a keypad key without text are
// the characters they type.
var paneNamed = map[string]string{
	"esc": "Escape", "space": "Space", "comma": "Comma",
	"equal": "=", "mul": "*", "plus": "+", "minus": "-", "period": ".", "div": "/",
}

// paneKnown is every named key send-keys takes (namedKeys and keyAliases),
// as Bubble Tea spells it.
var paneKnown = func() map[string]bool {
	m := map[string]bool{}
	for _, k := range strings.Fields("enter tab backspace up down left right home end pgup pgdown insert delete Escape Space Comma") {
		m[k] = true
	}
	for i := 1; i <= 12; i++ {
		m[fmt.Sprintf("f%d", i)] = true
	}
	return m
}()

// paneUnknown is every Bubble Tea key name send-keys has no key for: the
// kitty-only keys (ultraviolet's keyTypeString) and F13 to F63. A key in
// paneModifierKeys is a modifier pressed alone, which is dropped without a
// word, since nobody meant it as a key.
var (
	paneUnknown = func() map[string]bool {
		m := map[string]bool{}
		for _, k := range strings.Fields("begin find select sep capslock scrolllock numlock printscreen pause menu mediaplay mediapause mediaplaypause mediareverse mediastop mediafastforward mediarewind medianext mediaprev mediarecord lowervol raisevol mute isolevel3shift isolevel5shift") {
			m[k] = true
		}
		for i := 13; i <= 63; i++ {
			m[fmt.Sprintf("f%d", i)] = true
		}
		return m
	}()
	paneModifierKeys = func() map[string]bool {
		m := map[string]bool{}
		for _, side := range []string{"left", "right"} {
			for _, mod := range []string{"shift", "alt", "ctrl", "super", "hyper", "meta"} {
				m[side+mod] = true
			}
		}
		return m
	}()
)

// paneKeyVerdict is what becomes of one key on its way to send-keys.
type paneKeyVerdict int

const (
	keySend      paneKeyVerdict = iota // send the token
	keyUnsent                          // send-keys cannot express it: drop it and say so once
	keyQuietDrop                       // a modifier alone: drop it silently
)

// paneKey is the send-keys token for a Bubble Tea key name.
func paneKey(name string) (string, paneKeyVerdict) {
	if name == " " {
		return "Space", keySend
	}
	mods, key := splitPaneKey(name)
	if mods == nil {
		// No modifier list: a character, a named key or typed text.
		return paneBareKey(name)
	}
	var ctrl, alt, shift bool
	for _, m := range mods {
		switch m {
		case "ctrl":
			ctrl = true
		case "alt":
			alt = true
		case "shift":
			shift = true
		default: // meta, hyper, super: a terminal has no encoding for them
			if paneModifierKeys[key] {
				return "", keyQuietDrop
			}
			return "", keyUnsent
		}
	}
	if paneModifierKeys[key] {
		return "", keyQuietDrop
	}
	if alias, ok := paneNamed[key]; ok {
		key = alias
	}
	r, size := utf8.DecodeRuneInString(key)
	if size == len(key) && size > 0 {
		// One character with modifiers. shift on a symbol is the symbol it
		// types (a letter keeps its shift: send-keys encodes ctrl+shift+a
		// for a pane that reads the kitty protocol, ctrl+a for one that
		// does not).
		if shift && !unicode.IsLetter(r) {
			if s, ok := paneShifted[r]; ok {
				r = s
			}
			shift = false
		}
		// A comma goes as the named key Comma, which takes any modifier.
		if ctrl && r != ',' && !isASCIILetter(r) && !strings.ContainsRune(paneCtrlOK, r) {
			c, ok := paneCtrl[r]
			if !ok {
				return "", keyUnsent
			}
			r = c
		}
		key = string(r)
		switch {
		case key == ",":
			key = "Comma" // send-keys splits on commas
		case key == " ":
			key = "Space"
		case !ctrl && !alt && !shift:
			return key, keySend
		}
	} else if !paneKnown[key] {
		return "", keyUnsent
	}
	var b strings.Builder
	for _, m := range []struct {
		on   bool
		name string
	}{{ctrl, "ctrl+"}, {alt, "alt+"}, {shift, "shift+"}} {
		if m.on {
			b.WriteString(m.name)
		}
	}
	b.WriteString(key)
	return b.String(), keySend
}

// paneBareKey is the token for a key name with no modifier.
func paneBareKey(name string) (string, paneKeyVerdict) {
	switch {
	case name == ",":
		return "Comma", keySend
	case utf8.RuneCountInString(name) == 1:
		return name, keySend
	case paneModifierKeys[name]:
		return "", keyQuietDrop
	case paneUnknown[name]:
		return "", keyUnsent
	}
	if alias, ok := paneNamed[name]; ok {
		return alias, keySend
	}
	// A named key send-keys knows, or a key's text of several runes, which
	// send-keys types as its characters.
	return name, keySend
}

// splitPaneKey splits a keystroke into its modifiers and its key; mods is
// nil for a name with no modifier. "ctrl++" is ctrl and the plus key.
func splitPaneKey(name string) (mods []string, key string) {
	if !strings.Contains(name, "+") || utf8.RuneCountInString(name) == 1 {
		return nil, name
	}
	rest := name
	if strings.HasSuffix(rest, "++") {
		rest, key = rest[:len(rest)-2], "+"
	} else {
		i := strings.LastIndex(rest, "+")
		rest, key = rest[:i], rest[i+1:]
	}
	if key == "" || rest == "" {
		return nil, name
	}
	return strings.Split(rest, "+"), key
}

func isASCIILetter(r rune) bool { return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' }

// sendPaneKeys sends the keys send-keys can express through send, in order
// and in one call, and hands each key it cannot to unsendable (nil: none
// is told) instead of failing the whole press. Nothing to send is no call.
func sendPaneKeys(keys []string, unsendable func(key string), send func(ctx context.Context, tokens string) error) error {
	tokens := make([]string, 0, len(keys))
	for _, k := range keys {
		tok, verdict := paneKey(k)
		switch verdict {
		case keySend:
			tokens = append(tokens, tok)
		case keyUnsent:
			if unsendable != nil {
				unsendable(k)
			}
		}
	}
	if len(tokens) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), daemonTimeout)
	defer cancel()
	return send(ctx, strings.Join(tokens, " "))
}

// keyReporter is a stream that tells the app about a key it could not send
// (liveStream); App.openStream wires every stream mtm draws to
// App.unsendableKey.
type keyReporter interface {
	reportUnsendable(fn func(key string))
}

// unsendableKey tells the user once per key per run that the pane cannot
// be sent it: a status line, never an error, so repeating the key is quiet.
func (a *App) unsendableKey(key string) {
	if a.unsendable[key] {
		return
	}
	if a.unsendable == nil {
		a.unsendable = map[string]bool{}
	}
	a.unsendable[key] = true
	a.r.UI().Error(fmt.Sprintf("key %s can't be sent to the pane", key))
}
