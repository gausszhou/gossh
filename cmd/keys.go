package cmd

import (
	"fmt"
	"strings"
)

// Named-key translation for `gossh session press`.
//
// The server's POST /api/sessions/{id}/keys endpoint takes raw bytes on
// purpose and does no key-name translation (internal/api/agent_handler.go):
// an agent is expected to know the byte sequences. Humans and shell scripts
// are not, so the table lives here, on the client side — it is a port of
// terminal-use's src/keys.rs (MIT, https://github.com/flipbit03/terminal-use),
// which drives `tu press` with the same names:
//
//	"Enter" -> "\r"        "Ctrl+C"  -> "\x03"      "F1"      -> "\x1bOP"
//	"Up"    -> "\x1bOA"    "Alt+f"   -> "\x1b"+"f"  "Shift+Up" -> "\x1b[1;2A"

// namedKeys maps the plain (unmodified) key names to their byte sequences.
// Navigation uses the SS3 forms (ESC O A…) and F1-F4 the SS3 forms (ESC O P…),
// matching the xterm-256color terminfo entry the sessions advertise.
var namedKeys = map[string][]byte{
	// Editing
	"enter":     []byte("\r"),
	"return":    []byte("\r"),
	"tab":       []byte("\t"),
	"escape":    []byte("\x1b"),
	"esc":       []byte("\x1b"),
	"space":     []byte(" "),
	"backspace": []byte("\x7f"),
	"delete":    []byte("\x1b[3~"),
	"del":       []byte("\x1b[3~"),
	"insert":    []byte("\x1b[2~"),
	"ins":       []byte("\x1b[2~"),

	// Navigation
	"up":       []byte("\x1bOA"),
	"down":     []byte("\x1bOB"),
	"right":    []byte("\x1bOC"),
	"left":     []byte("\x1bOD"),
	"home":     []byte("\x1bOH"),
	"end":      []byte("\x1bOF"),
	"pageup":   []byte("\x1b[5~"),
	"pgup":     []byte("\x1b[5~"),
	"pagedown": []byte("\x1b[6~"),
	"pgdn":     []byte("\x1b[6~"),
	"pgdown":   []byte("\x1b[6~"),

	// Function keys
	"f1":  []byte("\x1bOP"),
	"f2":  []byte("\x1bOQ"),
	"f3":  []byte("\x1bOR"),
	"f4":  []byte("\x1bOS"),
	"f5":  []byte("\x1b[15~"),
	"f6":  []byte("\x1b[17~"),
	"f7":  []byte("\x1b[18~"),
	"f8":  []byte("\x1b[19~"),
	"f9":  []byte("\x1b[20~"),
	"f10": []byte("\x1b[21~"),
	"f11": []byte("\x1b[23~"),
	"f12": []byte("\x1b[24~"),
}

// tildeKeys are the keys modified as CSI {code};{mod}~ .
var tildeKeys = map[string]int{
	"insert": 2, "ins": 2,
	"delete": 3, "del": 3,
	"pageup": 5, "pgup": 5,
	"pagedown": 6, "pgdn": 6, "pgdown": 6,
	"f5": 15, "f6": 17, "f7": 18, "f8": 19,
	"f9": 20, "f10": 21, "f11": 23, "f12": 24,
}

// navFinal are the keys modified as CSI 1;{mod}{final} .
var navFinal = map[string]byte{
	"up": 'A', "down": 'B', "right": 'C', "left": 'D', "home": 'H', "end": 'F',
}

// fKeyFinal are F1-F4, modified as CSI 1;{mod}{final} .
var fKeyFinal = map[string]byte{"f1": 'P', "f2": 'Q', "f3": 'R', "f4": 'S'}

// resolveKey maps one key name to the bytes a PTY expects. A single
// character (of any script) resolves to its own UTF-8 bytes.
func resolveKey(name string) ([]byte, error) {
	lower := strings.ToLower(name)
	if b, ok := resolveModifierCombo(lower); ok {
		return b, nil
	}
	if b, ok := namedKeys[lower]; ok {
		return b, nil
	}
	if rs := []rune(name); len(rs) == 1 {
		return []byte(string(rs[0])), nil
	}
	return nil, fmt.Errorf("unknown key %q: valid keys are Enter, Tab, Escape, Space, "+
		"Backspace, Delete, Insert, Up, Down, Left, Right, Home, End, PageUp, PageDown, "+
		"F1-F12, Ctrl+<key>, Alt+<key>, Shift+<key>, or a single character", name)
}

// resolveKeys concatenates a key sequence (`press Down Down Enter`).
func resolveKeys(names []string) ([]byte, error) {
	var out []byte
	for _, n := range names {
		b, err := resolveKey(n)
		if err != nil {
			return nil, err
		}
		out = append(out, b...)
	}
	return out, nil
}

// resolveModifierCombo handles "Ctrl+C", "Alt+f", "Shift+Tab",
// "Ctrl+Shift+Up" and friends. ok=false means "not a combination I know",
// so the caller falls through to the plain-key table (which is how a lone
// "+" or "a" still resolves).
func resolveModifierCombo(lower string) ([]byte, bool) {
	parts := strings.Split(lower, "+")
	if len(parts) < 2 {
		return nil, false
	}
	key := parts[len(parts)-1]
	var ctrl, alt, shift bool
	for _, m := range parts[:len(parts)-1] {
		switch m {
		case "ctrl":
			ctrl = true
		case "alt", "meta":
			alt = true
		case "shift":
			shift = true
		default:
			return nil, false
		}
	}

	// Ctrl+<letter> -> control code 0x01-0x1a
	if ctrl && !alt && !shift {
		if rs := []rune(key); len(rs) == 1 && rs[0] >= 'a' && rs[0] <= 'z' {
			return []byte{byte(rs[0]-'a') + 1}, true
		}
	}

	// Alt+<key> -> ESC prefix
	if alt && !ctrl && !shift {
		if rs := []rune(key); len(rs) == 1 {
			return prependEsc([]byte(string(rs[0]))), true
		}
		if b, ok := namedKeys[key]; ok {
			return prependEsc(b), true
		}
	}

	// Shift+Tab -> backtab
	if shift && !ctrl && !alt && key == "tab" {
		return []byte("\x1b[Z"), true
	}

	// Modified navigation / function keys.
	mod, ok := modifierCode(ctrl, alt, shift)
	if !ok {
		return nil, false
	}
	if final, ok := navFinal[key]; ok {
		return []byte(fmt.Sprintf("\x1b[1;%d%c", mod, final)), true
	}
	if code, ok := tildeKeys[key]; ok {
		return []byte(fmt.Sprintf("\x1b[%d;%d~", code, mod)), true
	}
	if final, ok := fKeyFinal[key]; ok {
		return []byte(fmt.Sprintf("\x1b[1;%d%c", mod, final)), true
	}
	return nil, false
}

// modifierCode is the xterm modifier parameter: 2=Shift, 3=Alt, 4=Shift+Alt,
// 5=Ctrl, 6=Ctrl+Shift, 7=Ctrl+Alt, 8=Ctrl+Shift+Alt.
func modifierCode(ctrl, alt, shift bool) (int, bool) {
	switch {
	case !ctrl && !alt && shift:
		return 2, true
	case !ctrl && alt && !shift:
		return 3, true
	case !ctrl && alt && shift:
		return 4, true
	case ctrl && !alt && !shift:
		return 5, true
	case ctrl && !alt && shift:
		return 6, true
	case ctrl && alt && !shift:
		return 7, true
	case ctrl && alt && shift:
		return 8, true
	}
	return 0, false
}

// prependEsc returns ESC + b as a fresh slice (never aliasing b).
func prependEsc(b []byte) []byte {
	out := make([]byte, 0, 1+len(b))
	out = append(out, 0x1b)
	return append(out, b...)
}
