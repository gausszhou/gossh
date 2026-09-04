//go:build linux

package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func autostartFile(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return filepath.Join(home, ".config", "autostart", "gossh.desktop")
}

func TestSetAutostartToggle(t *testing.T) {
	p := autostartFile(t)
	if IsAutostart() {
		t.Fatal("IsAutostart should be false at start")
	}

	if err := SetAutostart(true); err != nil {
		t.Fatalf("SetAutostart(true): %v", err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("autostart file should exist: %v", err)
	}
	content := string(data)
	for _, want := range []string{
		"[Desktop Entry]",
		"Type=Application",
		"Name=GoSSH",
		"app --no-browser",
		"X-GNOME-Autostart-enabled=true",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("autostart content missing %q:\n%s", want, content)
		}
	}
	if !IsAutostart() {
		t.Fatal("IsAutostart should be true after SetAutostart(true)")
	}

	if err := SetAutostart(false); err != nil {
		t.Fatalf("SetAutostart(false): %v", err)
	}
	if IsAutostart() {
		t.Fatal("IsAutostart should be false after SetAutostart(false)")
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("autostart file should be removed, stat err=%v", err)
	}
}

func TestExecLinePrefersAppImage(t *testing.T) {
	t.Setenv("APPIMAGE", "/home/u/Apps/GoSSH.AppImage")
	line, err := execLine()
	if err != nil {
		t.Fatalf("execLine: %v", err)
	}
	if line != "/home/u/Apps/GoSSH.AppImage" {
		t.Fatalf("execLine should prefer $APPIMAGE, got %q", line)
	}
}
