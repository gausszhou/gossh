package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// ---------------------------------------------------------------------------
// known-hosts
// ---------------------------------------------------------------------------

func TestKnownHostsLsAndForget(t *testing.T) {
	ts := newAPIStub(t, nil)
	cmd := buildKnownHostsCmd()
	out, err := runCmd(t, cmd, "ls", "--server", ts.URL, "--token", testToken, "--json=false")
	if err != nil {
		t.Fatalf("known-hosts ls: %s", err)
	}
	for _, want := range []string{"ADDRESS", "10.0.0.5:22", "ssh-ed25519", "SHA256:aaa"} {
		if !strings.Contains(out, want) {
			t.Errorf("known-hosts ls is missing %q:\n%s", want, out)
		}
	}

	cmd = buildKnownHostsCmd()
	if _, err := runCmd(t, cmd, "forget", "10.0.0.5:22", "--json=false",
		"--server", ts.URL, "--token", testToken); err != nil {
		t.Fatalf("known-hosts forget: %s", err)
	}
	if got := ts.last("forget")["addr"]; got != "10.0.0.5:22" {
		t.Errorf("forgot %v, want the address verbatim", got)
	}
	ts.mu.Lock()
	remaining := len(ts.pins)
	ts.mu.Unlock()
	if remaining != 0 {
		t.Errorf("the pin survived: %d left", remaining)
	}
}

func TestKnownHostsForgetEscapesPathSegment(t *testing.T) {
	ts := newAPIStub(t, nil)
	cmd := buildKnownHostsCmd()
	// An IPv6 literal must survive as one path segment.
	if _, err := runCmd(t, cmd, "forget", "[::1]:22", "--json=false",
		"--server", ts.URL, "--token", testToken); err != nil {
		t.Fatalf("known-hosts forget: %s", err)
	}
	if got := ts.last("forget")["addr"]; got != "[::1]:22" {
		t.Errorf("addr came back as %v, want [::1]:22", got)
	}
}

// ---------------------------------------------------------------------------
// secrets
// ---------------------------------------------------------------------------

func TestSecretsSetPassword(t *testing.T) {
	ts := newAPIStub(t, nil)
	cmd := buildSecretsCmd()
	if _, err := runCmd(t, cmd, "set", "--kind", "password", "--addr", "10.0.0.5:22", "--user", "root",
		"--secret", "pw", "--json=false", "--server", ts.URL, "--token", testToken); err != nil {
		t.Fatalf("secrets set: %s", err)
	}
	body := ts.last("secret-set")
	if body["kind"] != "password" || body["addr"] != "10.0.0.5:22" || body["user"] != "root" || body["secret"] != "pw" {
		t.Errorf("posted secret = %+v", body)
	}
}

func TestSecretsSetRequiresSecret(t *testing.T) {
	cmd := buildSecretsCmd()
	_, err := runCmd(t, cmd, "set", "--kind", "password", "--addr", "a", "--user", "u",
		"--server", "http://127.0.0.1:1", "--token", "t")
	if err == nil || !strings.Contains(err.Error(), "--secret") {
		t.Errorf("want a --secret error, got %v", err)
	}
}

func TestSecretsSetPassphraseNeedsKey(t *testing.T) {
	cmd := buildSecretsCmd()
	_, err := runCmd(t, cmd, "set", "--kind", "passphrase", "--secret", "x",
		"--server", "http://127.0.0.1:1", "--token", "t")
	if err == nil || !strings.Contains(err.Error(), "--key") {
		t.Errorf("want a --key error, got %v", err)
	}
}

func TestSecretsRmSendsQueryParameters(t *testing.T) {
	ts := newAPIStub(t, nil)
	cmd := buildSecretsCmd()
	if _, err := runCmd(t, cmd, "rm", "--kind", "passphrase", "--key", "/keys/k", "--json=false",
		"--server", ts.URL, "--token", testToken); err != nil {
		t.Fatalf("secrets rm: %s", err)
	}
	if got := ts.last("secret-rm")["key_path"]; got != "/keys/k" {
		t.Errorf("key_path = %v, want /keys/k", got)
	}
}

// ---------------------------------------------------------------------------
// title
// ---------------------------------------------------------------------------

func TestTitleGetSetClear(t *testing.T) {
	ts := newAPIStub(t, nil)
	cmd := buildTitleCmd()
	out, err := runCmd(t, cmd, "get", "--server", ts.URL, "--token", testToken, "--json=false")
	if err != nil {
		t.Fatalf("title get: %s", err)
	}
	if strings.TrimSpace(out) != "GoSSH" {
		t.Errorf("title get = %q", out)
	}

	cmd = buildTitleCmd()
	if _, err := runCmd(t, cmd, "set", "My", "Fleet", "--json=false",
		"--server", ts.URL, "--token", testToken); err != nil {
		t.Fatalf("title set: %s", err)
	}
	if got := ts.last("title")["title"]; got != "My Fleet" {
		t.Errorf("posted title = %v", got)
	}

	cmd = buildTitleCmd()
	out, err = runCmd(t, cmd, "clear", "--json=false", "--server", ts.URL, "--token", testToken)
	if err != nil {
		t.Fatalf("title clear: %s", err)
	}
	if !strings.Contains(out, "Cleared") {
		t.Errorf("clear output = %q", out)
	}
	ts.mu.Lock()
	title := ts.title
	ts.mu.Unlock()
	if title != "" {
		t.Errorf("title = %q, want it cleared", title)
	}
}

// ---------------------------------------------------------------------------
// shared plumbing
// ---------------------------------------------------------------------------

func TestNewGroupsRequireToken(t *testing.T) {
	ts := newAPIStub(t, sampleHosts())
	for name, group := range map[string]*cobra.Command{
		"hosts":       buildHostsCmd(),
		"known-hosts": buildKnownHostsCmd(),
		"title":       buildTitleCmd(),
	} {
		t.Run(name, func(t *testing.T) {
			args := []string{"ls"}
			if name == "title" {
				args = []string{"get"}
			}
			_, err := runCmd(t, group, append(args, "--server", ts.URL, "--token", "wrong", "--json=false")...)
			if err == nil || !strings.Contains(err.Error(), "unauthorized") {
				t.Fatalf("want an unauthorized error, got %v", err)
			}
			if !strings.Contains(err.Error(), "--token") {
				t.Errorf("the error should hint at --token, got %v", err)
			}
		})
	}
}
