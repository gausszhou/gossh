package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gausszhou/gossh/internal/host"
)

// ---------------------------------------------------------------------------
// hosts
// ---------------------------------------------------------------------------

func TestResolveHostNameIsCaseInsensitive(t *testing.T) {
	ts := newAPIStub(t, sampleHosts())
	id, err := stubAPIClient(ts).resolveHost("PROD")
	if err != nil {
		t.Fatalf("resolveHost(PROD): %s", err)
	}
	if id != "h_1" {
		t.Errorf("resolved %s, want h_1", id)
	}
}

func TestResolveHostAmbiguousName(t *testing.T) {
	hosts := sampleHosts()
	hosts = append(hosts, host.Host{ID: "h_3", Name: "Prod", Address: "10.0.0.7", User: "root"})
	ts := newAPIStub(t, hosts)
	_, err := stubAPIClient(ts).resolveHost("prod")
	if err == nil || !strings.Contains(err.Error(), "use the host id") {
		t.Errorf("want an ambiguity error, got %v", err)
	}
}

func TestHostsLsHumanAndJSON(t *testing.T) {
	ts := newAPIStub(t, sampleHosts())

	cmd := buildHostsCmd()
	out, err := runCmd(t, cmd, "ls", "--server", ts.URL, "--token", testToken, "--json=false")
	if err != nil {
		t.Fatalf("hosts ls: %s", err)
	}
	for _, want := range []string{"ID", "NAME", "root@10.0.0.5:22", "prod", "staging", "Local (builtin)"} {
		if !strings.Contains(out, want) {
			t.Errorf("hosts ls is missing %q:\n%s", want, out)
		}
	}

	cmd = buildHostsCmd()
	out, err = runCmd(t, cmd, "list", "--server", ts.URL, "--token", testToken, "--json")
	if err != nil {
		t.Fatalf("hosts list --json: %s", err)
	}
	var list []host.Host
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("hosts ls --json did not emit a JSON array (%s):\n%s", err, out)
	}
	if len(list) != 3 { // builtin + 2
		t.Errorf("got %d hosts, want 3", len(list))
	}
}

func TestHostsAddPostsCredential(t *testing.T) {
	ts := newAPIStub(t, nil)
	cmd := buildHostsCmd()
	out, err := runCmd(t, cmd, "add",
		"--name", "new", "--address", "10.0.0.9", "--user", "deploy",
		"--key", "~/.ssh/deploy", "--json",
		"--server", ts.URL, "--token", testToken)
	if err != nil {
		t.Fatalf("hosts add: %s", err)
	}
	created, ok := ts.hostByID("h_new")
	if !ok {
		t.Fatal("the host was not stored")
	}
	if created.Credential.Kind != host.CredKey || created.Credential.KeyPath != "~/.ssh/deploy" {
		t.Errorf("credential = %+v, want kind=key with the key path", created.Credential)
	}
	var echoed host.Host
	if err := json.Unmarshal([]byte(out), &echoed); err != nil {
		t.Fatalf("add --json did not emit a host (%s):\n%s", err, out)
	}
	if echoed.Name != "new" {
		t.Errorf("echoed host = %+v", echoed)
	}
}

func TestHostsAddStoresPasswordFromFlag(t *testing.T) {
	ts := newAPIStub(t, nil)
	cmd := buildHostsCmd()
	if _, err := runCmd(t, cmd, "add",
		"--name", "new", "--address", "10.0.0.9", "--user", "deploy",
		"--password", "--secret", "hunter2", "--json=false",
		"--server", ts.URL, "--token", testToken); err != nil {
		t.Fatalf("hosts add: %s", err)
	}
	recorded := ts.last("host-password")
	if recorded["password"] != "hunter2" {
		t.Errorf("posted password = %v, want it carried to the keyring", recorded["password"])
	}
}

func TestHostsAddReadsSecretFromStdin(t *testing.T) {
	withStdin(t, "s3cr3t\n")
	ts := newAPIStub(t, nil)
	cmd := buildHostsCmd()
	if _, err := runCmd(t, cmd, "add",
		"--name", "new", "--address", "10.0.0.9", "--user", "deploy",
		"--password", "--secret", "-", "--json=false",
		"--server", ts.URL, "--token", testToken); err != nil {
		t.Fatalf("hosts add: %s", err)
	}
	if got := ts.last("host-password")["password"]; got != "s3cr3t" {
		t.Errorf("password from stdin = %v, want the trailing newline trimmed", got)
	}
}

func TestHostsAddStoresPassphraseViaSecrets(t *testing.T) {
	ts := newAPIStub(t, nil)
	cmd := buildHostsCmd()
	if _, err := runCmd(t, cmd, "add",
		"--name", "new", "--address", "10.0.0.9", "--user", "deploy",
		"--key", "/keys/deploy", "--secret", "pp", "--json=false",
		"--server", ts.URL, "--token", testToken); err != nil {
		t.Fatalf("hosts add: %s", err)
	}
	body := ts.last("secret-set")
	if body["kind"] != "passphrase" || body["key_path"] != "/keys/deploy" || body["secret"] != "pp" {
		t.Errorf("secrets call = %+v, want a passphrase for /keys/deploy", body)
	}
}

func TestHostsAddRequiresNameAddressUser(t *testing.T) {
	cmd := buildHostsCmd()
	_, err := runCmd(t, cmd, "add", "--name", "x", "--server", "http://127.0.0.1:1", "--token", "t")
	if err == nil || !strings.Contains(err.Error(), "--address") {
		t.Errorf("want a missing-flag error, got %v", err)
	}
}

func TestHostsCredentialFlagsAreMutuallyExclusive(t *testing.T) {
	cmd := buildHostsCmd()
	_, err := runCmd(t, cmd, "add", "--name", "n", "--address", "a", "--user", "u",
		"--key", "/k", "--agent", "--server", "http://127.0.0.1:1", "--token", "t")
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("want a mutual-exclusion error, got %v", err)
	}
}

func TestHostsEditAppliesOnlyChangedFlags(t *testing.T) {
	ts := newAPIStub(t, sampleHosts())
	cmd := buildHostsCmd()
	if _, err := runCmd(t, cmd, "edit", "staging", "--name", "staging-2", "--json=false",
		"--server", ts.URL, "--token", testToken); err != nil {
		t.Fatalf("hosts edit: %s", err)
	}
	updated, ok := ts.hostByID("h_2")
	if !ok {
		t.Fatal("the host disappeared")
	}
	if updated.Name != "staging-2" {
		t.Errorf("name = %q, want the new one", updated.Name)
	}
	// Everything the user did not mention must survive the round trip.
	if updated.Address != "10.0.0.6" || updated.Port != 2222 || updated.User != "ops" {
		t.Errorf("unmentioned fields were lost: %+v", updated)
	}
	if updated.Credential.Kind != host.CredAgent {
		t.Errorf("credential = %+v, want it kept", updated.Credential)
	}
	if len(updated.Forwards) != 1 {
		t.Errorf("forwards = %+v, want them kept", updated.Forwards)
	}
}

func TestHostsEditReplacesCredentialWhenAsked(t *testing.T) {
	ts := newAPIStub(t, sampleHosts())
	cmd := buildHostsCmd()
	if _, err := runCmd(t, cmd, "edit", "h_1", "--password", "--json=false",
		"--server", ts.URL, "--token", testToken); err != nil {
		t.Fatalf("hosts edit: %s", err)
	}
	updated, _ := ts.hostByID("h_1")
	if updated.Credential.Kind != host.CredPassword {
		t.Errorf("credential = %+v, want kind=password", updated.Credential)
	}
	if updated.Credential.KeyPath != "" {
		t.Errorf("the old key path leaked into the new credential: %+v", updated.Credential)
	}
}

func TestHostsShowHumanAndJSON(t *testing.T) {
	ts := newAPIStub(t, sampleHosts())
	cmd := buildHostsCmd()
	out, err := runCmd(t, cmd, "show", "prod", "--server", ts.URL, "--token", testToken, "--json=false")
	if err != nil {
		t.Fatalf("hosts show: %s", err)
	}
	if !strings.Contains(out, "10.0.0.5:22") || !strings.Contains(out, "key (~/.ssh/id_ed25519)") {
		t.Errorf("hosts show output = %q", out)
	}

	cmd = buildHostsCmd()
	out, err = runCmd(t, cmd, "show", "h_1", "--server", ts.URL, "--token", testToken, "--json")
	if err != nil {
		t.Fatalf("hosts show --json: %s", err)
	}
	var h host.Host
	if err := json.Unmarshal([]byte(out), &h); err != nil {
		t.Fatalf("show --json did not emit a host (%s):\n%s", err, out)
	}
	if h.ID != "h_1" {
		t.Errorf("showed %q, want h_1", h.ID)
	}
}

func TestHostsRemoveResolvesName(t *testing.T) {
	ts := newAPIStub(t, sampleHosts())
	cmd := buildHostsCmd()
	out, err := runCmd(t, cmd, "rm", "prod", "--server", ts.URL, "--token", testToken, "--json=false")
	if err != nil {
		t.Fatalf("hosts rm: %s", err)
	}
	if _, ok := ts.hostByID("h_1"); ok {
		t.Error("the host is still there")
	}
	if !strings.Contains(out, "h_1") {
		t.Errorf("rm should report the resolved id, got %q", out)
	}
}

func TestHostsBuiltinIsRejectedByServer(t *testing.T) {
	ts := newAPIStub(t, sampleHosts())
	cmd := buildHostsCmd()
	_, err := runCmd(t, cmd, "rm", "local", "--server", ts.URL, "--token", testToken, "--json=false")
	if err == nil || !strings.Contains(err.Error(), "built-in") {
		t.Errorf("want the server's builtin refusal surfaced, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// host forwards
// ---------------------------------------------------------------------------

func TestHostForwardsLsShowsStatusColumn(t *testing.T) {
	ts := newAPIStub(t, sampleHosts())
	cmd := buildHostsCmd()
	out, err := runCmd(t, cmd, "forwards", "ls", "staging", "--server", ts.URL, "--token", testToken, "--json=false")
	if err != nil {
		t.Fatalf("hosts forwards ls: %s", err)
	}
	for _, want := range []string{"KIND", "BIND", "STATUS", "127.0.0.1:8080", "running"} {
		if !strings.Contains(out, want) {
			t.Errorf("forwards ls is missing %q:\n%s", want, out)
		}
	}
}

func TestHostForwardsAddWritesHostRecord(t *testing.T) {
	ts := newAPIStub(t, sampleHosts())
	cmd := buildHostsCmd()
	if _, err := runCmd(t, cmd, "forwards", "add", "prod",
		"--kind", "local", "--bind", "127.0.0.1:9000", "--target", "localhost:80", "--json=false",
		"--server", ts.URL, "--token", testToken); err != nil {
		t.Fatalf("hosts forwards add: %s", err)
	}
	h, _ := ts.hostByID("h_1")
	if len(h.Forwards) != 1 || h.Forwards[0].Bind != "127.0.0.1:9000" {
		t.Errorf("host record forwards = %+v, want the new one", h.Forwards)
	}
	// The untouched sibling record keeps its own forwards.
	other, _ := ts.hostByID("h_2")
	if len(other.Forwards) != 1 || other.Forwards[0].Bind != "127.0.0.1:8080" {
		t.Errorf("the other host's forwards changed: %+v", other.Forwards)
	}
}

func TestHostForwardsAddValidatesTarget(t *testing.T) {
	cmd := buildHostsCmd()
	_, err := runCmd(t, cmd, "forwards", "add", "prod", "--kind", "local", "--bind", "127.0.0.1:9000",
		"--server", "http://127.0.0.1:1", "--token", "t")
	if err == nil || !strings.Contains(err.Error(), "--target") {
		t.Errorf("want a --target error for a local forward, got %v", err)
	}
}

func TestHostForwardsAddRejectsBadKind(t *testing.T) {
	cmd := buildHostsCmd()
	_, err := runCmd(t, cmd, "forwards", "add", "prod", "--kind", "socks", "--bind", "127.0.0.1:9000",
		"--server", "http://127.0.0.1:1", "--token", "t")
	if err == nil || !strings.Contains(err.Error(), "--kind") {
		t.Errorf("want a --kind error, got %v", err)
	}
}

func TestHostForwardsRmMatchesBind(t *testing.T) {
	ts := newAPIStub(t, sampleHosts())
	cmd := buildHostsCmd()
	if _, err := runCmd(t, cmd, "forwards", "rm", "staging", "--bind", "127.0.0.1:8080", "--json=false",
		"--server", ts.URL, "--token", testToken); err != nil {
		t.Fatalf("hosts forwards rm: %s", err)
	}
	h, _ := ts.hostByID("h_2")
	if len(h.Forwards) != 0 {
		t.Errorf("forwards = %+v, want none", h.Forwards)
	}
}

func TestHostForwardsRmUnknownBind(t *testing.T) {
	ts := newAPIStub(t, sampleHosts())
	cmd := buildHostsCmd()
	_, err := runCmd(t, cmd, "forwards", "rm", "prod", "--bind", "127.0.0.1:1", "--json=false",
		"--server", ts.URL, "--token", testToken)
	if err == nil || !strings.Contains(err.Error(), "no forward") {
		t.Errorf("want a no-match error, got %v", err)
	}
}

func TestFormatForwardsRendersFailureDetail(t *testing.T) {
	out := formatForwards([]forwardRow{
		{ID: "hf_0", Kind: "local", Bind: "127.0.0.1:1", Target: "localhost:2", Status: "pending"},
		{ID: "hf_1", Kind: "local", Bind: "127.0.0.1:3", Target: "localhost:4", Status: "failed", Error: "boom"},
	})
	for _, want := range []string{"pending", "failed: boom"} {
		if !strings.Contains(out, want) {
			t.Errorf("table is missing %q:\n%s", want, out)
		}
	}
}
