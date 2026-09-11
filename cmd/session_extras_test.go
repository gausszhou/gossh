package cmd

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// session rename / resize / signal
// ---------------------------------------------------------------------------

func TestSessionRename(t *testing.T) {
	ts := newAPIStub(t, nil)
	cmd := buildSessionCmd()
	if _, err := runCmd(t, cmd, "rename", "deploy", "window", "-s", "bbbb", "--json=false",
		"--server", ts.URL, "--token", testToken); err != nil {
		t.Fatalf("session rename: %s", err)
	}
	body := ts.last("session-title")
	if body["title"] != "deploy window" {
		t.Errorf("posted title = %v, want the words joined", body["title"])
	}
}

func TestSessionResizeValidatesBeforeSending(t *testing.T) {
	cmd := buildSessionCmd()
	_, err := runCmd(t, cmd, "resize", "--cols", "0", "--rows", "10",
		"--server", "http://127.0.0.1:1", "--token", "t")
	if err == nil || !strings.Contains(err.Error(), "--cols") {
		t.Errorf("want a positive-size error, got %v", err)
	}
}

func TestSessionResizePostsDimensions(t *testing.T) {
	ts := newAPIStub(t, nil)
	cmd := buildSessionCmd()
	if _, err := runCmd(t, cmd, "resize", "--cols", "200", "--rows", "50", "-s", "aaaa", "--json=false",
		"--server", ts.URL, "--token", testToken); err != nil {
		t.Fatalf("session resize: %s", err)
	}
	body := ts.last("session-resize")
	if body["width"] != float64(200) || body["height"] != float64(50) {
		t.Errorf("posted resize = %+v", body)
	}
}

func TestSessionSignalRejectsUnknownName(t *testing.T) {
	cmd := buildSessionCmd()
	_, err := runCmd(t, cmd, "signal", "SIGBOGUS", "--server", "http://127.0.0.1:1", "--token", "t")
	if err == nil || !strings.Contains(err.Error(), "SIGHUP") {
		t.Errorf("want an unknown-signal error listing the valid ones, got %v", err)
	}
}

func TestSessionSignalUppercasesAndPosts(t *testing.T) {
	ts := newAPIStub(t, nil)
	cmd := buildSessionCmd()
	if _, err := runCmd(t, cmd, "signal", "sigterm", "-s", "aaaa", "--json=false",
		"--server", ts.URL, "--token", testToken); err != nil {
		t.Fatalf("session signal: %s", err)
	}
	if got := ts.last("session-signal")["signal"]; got != "SIGTERM" {
		t.Errorf("posted signal = %v, want SIGTERM", got)
	}
}

// ---------------------------------------------------------------------------
// session forwards
// ---------------------------------------------------------------------------

func TestSessionForwardsLsEmptyThenAdd(t *testing.T) {
	ts := newAPIStub(t, nil)
	cmd := buildSessionCmd()
	out, err := runCmd(t, cmd, "forwards", "ls", "-s", "aaaa", "--server", ts.URL, "--token", testToken, "--json=false")
	if err != nil {
		t.Fatalf("session forwards ls: %s", err)
	}
	if !strings.Contains(out, "no forwards") {
		t.Errorf("empty listing should say so, got %q", out)
	}

	cmd = buildSessionCmd()
	if _, err := runCmd(t, cmd, "forwards", "add", "-s", "aaaa",
		"--kind", "dynamic", "--bind", "127.0.0.1:1080", "--json=false",
		"--server", ts.URL, "--token", testToken); err != nil {
		t.Fatalf("session forwards add: %s", err)
	}
	if got := ts.last("session-forward-add")["kind"]; got != "dynamic" {
		t.Errorf("posted kind = %v", got)
	}

	cmd = buildSessionCmd()
	out, err = runCmd(t, cmd, "forwards", "ls", "-s", "aaaa", "--server", ts.URL, "--token", testToken, "--json=false")
	if err != nil {
		t.Fatalf("session forwards ls: %s", err)
	}
	if !strings.Contains(out, "127.0.0.1:1080") || !strings.Contains(out, "f_new") {
		t.Errorf("the added forward is missing from the listing:\n%s", out)
	}
}

func TestSessionForwardsRm(t *testing.T) {
	ts := newAPIStub(t, nil)
	cmd := buildSessionCmd()
	if _, err := runCmd(t, cmd, "forwards", "rm", "f_1", "-s", "aaaa", "--json=false",
		"--server", ts.URL, "--token", testToken); err != nil {
		t.Fatalf("session forwards rm: %s", err)
	}
	if got := ts.last("session-forward-rm")["fid"]; got != "f_1" {
		t.Errorf("removed %v, want f_1", got)
	}
}
