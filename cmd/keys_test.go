package cmd

import (
	"bytes"
	"testing"
)

// The expectations below are the byte sequences terminal-use emits for the
// same names (src/keys.rs), so the two tools stay interchangeable.
func TestResolveKey(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		// Editing
		{"Enter", "\r"},
		{"enter", "\r"},
		{"return", "\r"},
		{"Tab", "\t"},
		{"Escape", "\x1b"},
		{"esc", "\x1b"},
		{"Space", " "},
		{"Backspace", "\x7f"},
		{"Delete", "\x1b[3~"},
		{"Insert", "\x1b[2~"},

		// Navigation (SS3 forms)
		{"Up", "\x1bOA"},
		{"Down", "\x1bOB"},
		{"Right", "\x1bOC"},
		{"Left", "\x1bOD"},
		{"Home", "\x1bOH"},
		{"End", "\x1bOF"},
		{"PageUp", "\x1b[5~"},
		{"pgdn", "\x1b[6~"},

		// Function keys
		{"F1", "\x1bOP"},
		{"F4", "\x1bOS"},
		{"F5", "\x1b[15~"},
		{"F12", "\x1b[24~"},

		// Modifiers
		{"Ctrl+C", "\x03"},
		{"ctrl+d", "\x04"},
		{"Ctrl+Z", "\x1a"},
		{"Alt+f", "\x1bf"},
		{"Meta+f", "\x1bf"},
		{"Alt+Up", "\x1b\x1bOA"},
		{"Shift+Tab", "\x1b[Z"},
		{"Shift+Up", "\x1b[1;2A"},
		{"Ctrl+Shift+Up", "\x1b[1;6A"},
		{"Ctrl+Alt+Delete", "\x1b[3;7~"},

		// Single characters, including non-ASCII and "+"
		{"a", "a"},
		{"Z", "Z"},
		{"!", "!"},
		{"+", "+"},
		{"中", "中"},
	}
	for _, tt := range tests {
		got, err := resolveKey(tt.key)
		if err != nil {
			t.Errorf("resolveKey(%q) unexpected error: %s", tt.key, err)
			continue
		}
		if string(got) != tt.want {
			t.Errorf("resolveKey(%q) = %q, want %q", tt.key, got, tt.want)
		}
	}
}

func TestResolveKeyRejectsUnknownNames(t *testing.T) {
	for _, key := range []string{"FooBar", "Ctrl+", "Ctrl+1", "Ctrl+Shift+C", ""} {
		if _, err := resolveKey(key); err == nil {
			t.Errorf("resolveKey(%q) = nil error, want an unknown-key error", key)
		}
	}
}

func TestResolveKeysSequence(t *testing.T) {
	got, err := resolveKeys([]string{"Down", "Down", "Enter"})
	if err != nil {
		t.Fatalf("resolveKeys: %s", err)
	}
	if want := "\x1bOB\x1bOB\r"; string(got) != want {
		t.Errorf("resolveKeys = %q, want %q", got, want)
	}

	// A vi-style save-and-quit sequence.
	got, err = resolveKeys([]string{"Escape", ":", "w", "q", "Enter"})
	if err != nil {
		t.Fatalf("resolveKeys: %s", err)
	}
	if want := "\x1b:wq\r"; string(got) != want {
		t.Errorf("resolveKeys = %q, want %q", got, want)
	}
}

func TestResolveKeysStopsOnUnknown(t *testing.T) {
	if _, err := resolveKeys([]string{"Enter", "Nope"}); err == nil {
		t.Error("want an error when a key in the middle of a sequence is unknown")
	}
}

// prependEsc builds a new slice per call: a mutated result must never leak
// back into the shared key table.
func TestResolveKeyDoesNotAliasTheTable(t *testing.T) {
	first, err := resolveKey("Alt+Up")
	if err != nil {
		t.Fatal(err)
	}
	first[0] = 'X'

	second, err := resolveKey("Alt+Up")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(second, []byte{0x1b}) {
		t.Errorf("the key table was aliased: Alt+Up now resolves to %q", second)
	}

	plain, err := resolveKey("Up")
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "\x1bOA" {
		t.Errorf("the named-key table was mutated: Up resolves to %q", plain)
	}
}
