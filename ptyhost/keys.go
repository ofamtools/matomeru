package ptyhost

import (
	"strings"
	"unicode/utf8"
)

// Encode turns a named key into the bytes an xterm sends for it. Names follow
// Bubble Tea's key strings: a printable character as itself ("a", "A", "é"),
// "space", "enter", "tab", "shift+tab", "backspace", "esc", "up", "down",
// "left", "right", "home", "end", "pgup", "pgdown", "insert", "delete",
// "f1".."f12", "ctrl+<letter>" and the ctrl punctuation keys, and "alt+<key>"
// for any of them. Arrows, home and end take ctrl, alt and shift modifiers.
// appCursor selects the application cursor key form (ESC O x) a program can
// ask for. It reports false for a name it cannot encode.
func Encode(key string, appCursor bool) ([]byte, bool) {
	if key == "" {
		return nil, false
	}
	if utf8.RuneCountInString(key) == 1 {
		return []byte(key), true
	}
	if b, ok := named[key]; ok {
		return []byte(b), true
	}
	if b, ok := cursorKey(key, appCursor); ok {
		return b, true
	}
	if rest, ok := strings.CutPrefix(key, "ctrl+"); ok {
		if b, ok := ctrlByte(rest); ok {
			return []byte{b}, true
		}
	}
	if rest, ok := strings.CutPrefix(key, "alt+"); ok {
		if b, ok := Encode(rest, appCursor); ok {
			return append([]byte{0x1b}, b...), true
		}
	}
	return nil, false
}

var named = map[string]string{
	"space": " ", "enter": "\r", "tab": "\t", "shift+tab": "\x1b[Z",
	"backspace": "\x7f", "esc": "\x1b", "escape": "\x1b",
	"insert": "\x1b[2~", "delete": "\x1b[3~", "pgup": "\x1b[5~", "pgdown": "\x1b[6~",
	"f1": "\x1bOP", "f2": "\x1bOQ", "f3": "\x1bOR", "f4": "\x1bOS",
	"f5": "\x1b[15~", "f6": "\x1b[17~", "f7": "\x1b[18~", "f8": "\x1b[19~",
	"f9": "\x1b[20~", "f10": "\x1b[21~", "f11": "\x1b[23~", "f12": "\x1b[24~",
}

var cursorFinal = map[string]byte{"up": 'A', "down": 'B', "right": 'C', "left": 'D', "home": 'H', "end": 'F'}

// cursorKey encodes an arrow, home or end with optional shift/alt/ctrl
// modifiers in xterm's CSI 1;m x form.
func cursorKey(key string, appCursor bool) ([]byte, bool) {
	parts := strings.Split(key, "+")
	fin, ok := cursorFinal[parts[len(parts)-1]]
	if !ok {
		return nil, false
	}
	mod := 1
	for _, p := range parts[:len(parts)-1] {
		switch p {
		case "shift":
			mod += 1
		case "alt":
			mod += 2
		case "ctrl":
			mod += 4
		default:
			return nil, false
		}
	}
	switch {
	case mod > 1:
		return []byte("\x1b[1;" + string(rune('0'+mod)) + string(fin)), true
	case appCursor:
		return []byte{0x1b, 'O', fin}, true
	}
	return []byte{0x1b, '[', fin}, true
}

func ctrlByte(k string) (byte, bool) {
	if len(k) == 1 {
		c := k[0]
		switch {
		case c >= 'a' && c <= 'z':
			return c - 'a' + 1, true
		case c >= '@' && c <= '_': // @ A..Z [ \ ] ^ _
			return c - '@', true
		case c == '?':
			return 0x7f, true
		}
	}
	if k == "space" {
		return 0, true
	}
	return 0, false
}
