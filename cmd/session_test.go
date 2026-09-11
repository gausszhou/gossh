package cmd

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"github.com/gausszhou/gossh/internal/host"
	"github.com/gausszhou/gossh/internal/session"
)

const testToken = "test-token"

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// buildTestSessionCmd returns a fresh `session` command carrying the same
// persistent --config flag the root command installs.
//
// cobra only folds a command's own persistent flags into cmd.Flags() while
// parsing, and the endpoint tests call apiEndpoint() directly instead of
// going through Execute, so parse an empty arg list to trigger the merge.
func buildTestSessionCmd(t *testing.T, configPath string) *cobra.Command {
	t.Helper()
	cmd := buildSessionCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.PersistentFlags().String("config", configPath, "")
	if err := cmd.ParseFlags(nil); err != nil {
		t.Fatalf("preparing the test command: %s", err)
	}
	return cmd
}

// captureStdout runs fn with os.Stdout redirected into a pipe.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	_ = w.Close()
	os.Stdout = old
	return <-done
}

func testSessions() []session.StateDescription {
	return []session.StateDescription{
		{
			ID:        "aaaaaaaaaaaaaaaa",
			State:     "running",
			Spec:      session.ConnectSpec{HostID: "h_1", Name: "prod", Addr: "10.0.0.5:22", User: "root"},
			CreatedAt: "2026-09-11T10:00:00Z",
		},
		{
			ID:        "bbbbbbbbbbbbbbbb",
			State:     "running",
			Spec:      session.ConnectSpec{HostID: "h_2", Name: "staging", Addr: "10.0.0.6:22", User: "ops"},
			Title:     "deploy",
			CreatedAt: "2026-09-11T11:00:00Z",
		},
	}
}

// stubServer stands in for a running gossh server: it serves the endpoints
// the CLI reads and remembers the bodies posted to the ones it writes.
type stubServer struct {
	*httptest.Server

	mu           sync.Mutex
	recorded     map[string]map[string]any
	waitTimedOut bool
}

func (s *stubServer) record(name string, body map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.recorded == nil {
		s.recorded = map[string]map[string]any{}
	}
	s.recorded[name] = body
}

func (s *stubServer) last(name string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recorded[name]
}

func (s *stubServer) setWaitTimedOut(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.waitTimedOut = v
}

func newStubServer(t *testing.T, sessions []session.StateDescription, hosts []host.Host) *stubServer {
	t.Helper()
	s := &stubServer{}

	authorized := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized: missing or invalid access token"}`))
			return false
		}
		return true
	}
	writeJSON := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	decode := func(r *http.Request) map[string]any {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body == nil {
			body = map[string]any{}
		}
		return body
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/sessions", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		writeJSON(w, map[string]any{"sessions": sessions})
	})
	mux.HandleFunc("GET /api/hosts", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		writeJSON(w, hosts)
	})
	mux.HandleFunc("POST /api/sessions", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		s.record("create", decode(r))
		writeJSON(w, session.StateDescription{
			ID:        "cccccccccccccccc",
			State:     "running",
			Spec:      session.ConnectSpec{HostID: "h_1", Name: "prod", Addr: "10.0.0.5:22", User: "root"},
			CreatedAt: "2026-09-11T12:00:00Z",
		})
	})
	mux.HandleFunc("POST /api/sessions/{id}/keys", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		s.record("keys", decode(r))
		writeJSON(w, map[string]int{"written": 1})
	})
	mux.HandleFunc("POST /api/sessions/{id}/wait", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		s.record("wait", decode(r))
		s.mu.Lock()
		timedOut := s.waitTimedOut
		s.mu.Unlock()
		writeJSON(w, map[string]any{
			"session_id": r.PathValue("id"),
			"text":       "done",
			"matched":    !timedOut,
			"quiet":      false,
			"timed_out":  timedOut,
		})
	})
	mux.HandleFunc("DELETE /api/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		s.record("destroy", map[string]any{"path": r.URL.Path})
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /api/sessions/{id}/screen", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		switch r.URL.Query().Get("format") {
		case "json":
			writeJSON(w, map[string]any{"session_id": r.PathValue("id"), "text": "hello from screen"})
		case "png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("\x89PNG\r\n\x1a\nfake-bitmap"))
		default:
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("hello from screen"))
		}
	})

	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func stubClient(ts *stubServer) *apiClient {
	return &apiClient{base: ts.URL, token: testToken, http: ts.Client()}
}

func runCmd(t *testing.T, cmd *cobra.Command, args ...string) (string, error) {
	t.Helper()
	cmd.SetArgs(args)
	var err error
	out := captureStdout(t, func() { err = cmd.Execute() })
	return out, err
}

// ---------------------------------------------------------------------------
// endpoint resolution
// ---------------------------------------------------------------------------

func TestSessionEndpointFromConfigFile(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte("config-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(dir, "config.json")
	body := `{"address":"0.0.0.0","port":"9999","token_file":` + strconv.Quote(tokenFile) + `}`
	if err := os.WriteFile(configFile, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	base, token, err := apiEndpoint(buildTestSessionCmd(t, configFile))
	if err != nil {
		t.Fatalf("apiEndpoint: %s", err)
	}
	if want := "http://127.0.0.1:9999"; base != want {
		t.Errorf("base = %q, want %q (0.0.0.0 must become 127.0.0.1)", base, want)
	}
	if token != "config-token" {
		t.Errorf("token = %q, want the token file contents trimmed", token)
	}
}

func TestSessionEndpointFlagsBeatConfig(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configFile, []byte(`{"port":"9999"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := buildTestSessionCmd(t, configFile)
	if err := cmd.PersistentFlags().Set("server", "https://example.test:1234/"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.PersistentFlags().Set("token", "flag-token"); err != nil {
		t.Fatal(err)
	}

	base, token, err := apiEndpoint(cmd)
	if err != nil {
		t.Fatalf("apiEndpoint: %s", err)
	}
	if want := "https://example.test:1234"; base != want {
		t.Errorf("base = %q, want %q (trailing slash trimmed)", base, want)
	}
	if token != "flag-token" {
		t.Errorf("token = %q, want the flag value", token)
	}
}

func TestSessionEndpointAddsSchemeAndHonoursEnvToken(t *testing.T) {
	t.Setenv("GOSSH_TOKEN", "env-token")
	cmd := buildTestSessionCmd(t, filepath.Join(t.TempDir(), "absent.json"))
	if err := cmd.PersistentFlags().Set("server", "127.0.0.1:8041"); err != nil {
		t.Fatal(err)
	}

	base, token, err := apiEndpoint(cmd)
	if err != nil {
		t.Fatalf("apiEndpoint: %s", err)
	}
	if want := "http://127.0.0.1:8041"; base != want {
		t.Errorf("base = %q, want %q (scheme added)", base, want)
	}
	if token != "env-token" {
		t.Errorf("token = %q, want GOSSH_TOKEN", token)
	}
}

func TestSessionEndpointRequiresToken(t *testing.T) {
	t.Setenv("GOSSH_TOKEN", "")
	t.Setenv("GOSSH_SERVER", "")
	cmd := buildTestSessionCmd(t, filepath.Join(t.TempDir(), "absent.json"))
	if err := cmd.PersistentFlags().Set("token-file", filepath.Join(t.TempDir(), "absent-token")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := apiEndpoint(cmd); err == nil {
		t.Error("want an error when no token can be found")
	}
}

func TestSessionEndpointRejectsRandomPort(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configFile, []byte(`{"port":"0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := buildTestSessionCmd(t, configFile)
	if err := cmd.PersistentFlags().Set("token", "t"); err != nil {
		t.Fatal(err)
	}
	_, _, err := apiEndpoint(cmd)
	if err == nil || !strings.Contains(err.Error(), "--server") {
		t.Errorf("want a --server hint for --port 0, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// session targeting
// ---------------------------------------------------------------------------

func TestResolveSessionTargeting(t *testing.T) {
	ts := newStubServer(t, testSessions(), nil)
	c := stubClient(ts)

	tests := []struct {
		name    string
		query   string
		wantID  string
		wantErr string
	}{
		{name: "exact id", query: "bbbbbbbbbbbbbbbb", wantID: "bbbbbbbbbbbbbbbb"},
		{name: "unique id prefix", query: "aaaa", wantID: "aaaaaaaaaaaaaaaa"},
		{name: "id prefix of length one", query: "b", wantID: "bbbbbbbbbbbbbbbb"},
		{name: "title", query: "deploy", wantID: "bbbbbbbbbbbbbbbb"},
		{name: "host name", query: "prod", wantID: "aaaaaaaaaaaaaaaa"},
		{name: "no match", query: "nope", wantErr: "no session matches"},
		{name: "omitted and ambiguous", query: "", wantErr: "--session/-s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := buildTestSessionCmd(t, "")
			if tt.query != "" {
				if err := cmd.PersistentFlags().Set("session", tt.query); err != nil {
					t.Fatal(err)
				}
			}
			got, err := c.resolveSession(cmd)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("resolveSession(%q) err = %v, want it to mention %q", tt.query, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveSession(%q): %s", tt.query, err)
			}
			if got.ID != tt.wantID {
				t.Errorf("resolveSession(%q) = %s, want %s", tt.query, got.ID, tt.wantID)
			}
		})
	}
}

func TestResolveSessionSingleAliveIsImplicit(t *testing.T) {
	ts := newStubServer(t, testSessions()[:1], nil)
	got, err := stubClient(ts).resolveSession(buildTestSessionCmd(t, ""))
	if err != nil {
		t.Fatalf("resolveSession: %s", err)
	}
	if got.ID != "aaaaaaaaaaaaaaaa" {
		t.Errorf("resolved %s, want the only session", got.ID)
	}
}

func TestResolveSessionNameMatchingIsCaseInsensitive(t *testing.T) {
	// The built-in local server's record is named "Local" while every other
	// surface calls it "local"; `-s local` must still find the session.
	sessions := testSessions()[:1]
	sessions[0].Spec.Name = "Local"
	ts := newStubServer(t, sessions, nil)
	cmd := buildTestSessionCmd(t, "")
	if err := cmd.PersistentFlags().Set("session", "local"); err != nil {
		t.Fatal(err)
	}
	got, err := stubClient(ts).resolveSession(cmd)
	if err != nil {
		t.Fatalf("resolveSession(local): %s", err)
	}
	if got.ID != sessions[0].ID {
		t.Errorf("resolved %s, want %s", got.ID, sessions[0].ID)
	}
}

func TestResolveSessionAmbiguousPrefix(t *testing.T) {
	sessions := testSessions()
	sessions[1].ID = "aaaa111111111111"
	ts := newStubServer(t, sessions, nil)
	cmd := buildTestSessionCmd(t, "")
	if err := cmd.PersistentFlags().Set("session", "aaaa"); err != nil {
		t.Fatal(err)
	}
	_, err := stubClient(ts).resolveSession(cmd)
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("want an ambiguous-prefix error, got %v", err)
	}
}

func TestResolveSessionNoSessions(t *testing.T) {
	ts := newStubServer(t, nil, nil)
	_, err := stubClient(ts).resolveSession(buildTestSessionCmd(t, ""))
	if err == nil || !strings.Contains(err.Error(), "no sessions") {
		t.Errorf("want a no-sessions error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// commands
// ---------------------------------------------------------------------------

func TestSessionLsHumanTable(t *testing.T) {
	ts := newStubServer(t, testSessions(), nil)
	cmd := buildTestSessionCmd(t, "")
	out, err := runCmd(t, cmd, "ls", "--server", ts.URL, "--token", testToken, "--json=false")
	if err != nil {
		t.Fatalf("ls: %s", err)
	}
	for _, want := range []string{"ID", "STATE", "HOST", "aaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbb", "prod", "deploy"} {
		if !strings.Contains(out, want) {
			t.Errorf("ls output is missing %q:\n%s", want, out)
		}
	}
	if strings.ContainsAny(out, "{}\"") {
		t.Errorf("human output should not look like JSON:\n%s", out)
	}
}

func TestSessionLsJSON(t *testing.T) {
	ts := newStubServer(t, testSessions(), nil)
	cmd := buildTestSessionCmd(t, "")
	out, err := runCmd(t, cmd, "ls", "--server", ts.URL, "--token", testToken, "--json")
	if err != nil {
		t.Fatalf("ls --json: %s", err)
	}
	var payload struct {
		Base     string                     `json:"base"`
		Sessions []session.StateDescription `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("ls --json did not emit JSON (%s):\n%s", err, out)
	}
	if payload.Base != ts.URL {
		t.Errorf("base = %q, want %q", payload.Base, ts.URL)
	}
	if len(payload.Sessions) != 2 {
		t.Errorf("got %d sessions, want 2", len(payload.Sessions))
	}
}

func TestSessionLsEmpty(t *testing.T) {
	ts := newStubServer(t, nil, nil)
	cmd := buildTestSessionCmd(t, "")
	out, err := runCmd(t, cmd, "ls", "--server", ts.URL, "--token", testToken, "--json=false")
	if err != nil {
		t.Fatalf("ls: %s", err)
	}
	if !strings.Contains(out, "no sessions") {
		t.Errorf("empty listing should say so, got:\n%s", out)
	}

	cmd = buildTestSessionCmd(t, "")
	out, err = runCmd(t, cmd, "ls", "--server", ts.URL, "--token", testToken, "--json")
	if err != nil {
		t.Fatalf("ls --json: %s", err)
	}
	// An empty listing must still be valid JSON with an empty array.
	if !strings.Contains(out, `"sessions": []`) {
		t.Errorf("empty --json listing should emit [], got:\n%s", out)
	}
}

func TestSessionCreateResolvesHostByName(t *testing.T) {
	hosts := []host.Host{{ID: "h_1", Name: "prod", Address: "10.0.0.5", User: "root"}}
	ts := newStubServer(t, testSessions(), hosts)
	cmd := buildTestSessionCmd(t, "")
	out, err := runCmd(t, cmd, "create", "--host", "prod", "--json", "--server", ts.URL, "--token", testToken)
	if err != nil {
		t.Fatalf("create: %s", err)
	}
	body := ts.last("create")
	if body["host_id"] != "h_1" {
		t.Errorf("posted host_id = %v, want the resolved id h_1", body["host_id"])
	}
	var created session.StateDescription
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		t.Fatalf("create --json did not emit JSON (%s):\n%s", err, out)
	}
	if created.ID != "cccccccccccccccc" {
		t.Errorf("created id = %q", created.ID)
	}
}

func TestSessionCreateRequiresHost(t *testing.T) {
	cmd := buildTestSessionCmd(t, "")
	if _, err := runCmd(t, cmd, "create", "--server", "http://127.0.0.1:1", "--token", "t"); err == nil {
		t.Error("want an error when --host is missing")
	}
}

func TestSessionPressSendsBase64Bytes(t *testing.T) {
	ts := newStubServer(t, testSessions()[:1], nil)
	cmd := buildTestSessionCmd(t, "")
	if _, err := runCmd(t, cmd, "press", "Down", "Enter", "--server", ts.URL, "--token", testToken); err != nil {
		t.Fatalf("press: %s", err)
	}
	body := ts.last("keys")
	if body["encoding"] != "base64" {
		t.Errorf("encoding = %v, want base64", body["encoding"])
	}
	encoded, _ := body["input"].(string)
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("input is not base64 (%s): %q", err, encoded)
	}
	if want := "\x1bOB\r"; string(decoded) != want {
		t.Errorf("sent %q, want %q", decoded, want)
	}
}

func TestSessionPressRejectsUnknownKeyBeforeSending(t *testing.T) {
	ts := newStubServer(t, testSessions()[:1], nil)
	cmd := buildTestSessionCmd(t, "")
	if _, err := runCmd(t, cmd, "press", "Nope", "--server", ts.URL, "--token", testToken); err == nil {
		t.Error("want an unknown-key error")
	}
	if ts.last("keys") != nil {
		t.Error("nothing should have been sent for an unknown key")
	}
}

func TestSessionTypeAppendsEnter(t *testing.T) {
	ts := newStubServer(t, testSessions()[:1], nil)
	cmd := buildTestSessionCmd(t, "")
	if _, err := runCmd(t, cmd, "type", "echo hi", "--enter", "--server", ts.URL, "--token", testToken); err != nil {
		t.Fatalf("type: %s", err)
	}
	body := ts.last("keys")
	if body["encoding"] != "text" {
		t.Errorf("encoding = %v, want text", body["encoding"])
	}
	if body["input"] != "echo hi\r" {
		t.Errorf("input = %q, want %q", body["input"], "echo hi\r")
	}
}

func TestSessionTypeJoinsUnquotedWords(t *testing.T) {
	ts := newStubServer(t, testSessions()[:1], nil)
	cmd := buildTestSessionCmd(t, "")
	if _, err := runCmd(t, cmd, "type", "echo", "hello", "world", "--server", ts.URL, "--token", testToken); err != nil {
		t.Fatalf("type: %s", err)
	}
	if body := ts.last("keys"); body["input"] != "echo hello world" {
		t.Errorf("input = %q, want the words joined by a space", body["input"])
	}
}

func TestSessionScreenText(t *testing.T) {
	ts := newStubServer(t, testSessions()[:1], nil)
	cmd := buildTestSessionCmd(t, "")
	out, err := runCmd(t, cmd, "screen", "--server", ts.URL, "--token", testToken, "--json=false")
	if err != nil {
		t.Fatalf("screen: %s", err)
	}
	if !strings.Contains(out, "hello from screen") {
		t.Errorf("screen output = %q", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("screen output should end with a newline: %q", out)
	}
}

func TestSessionScreenJSONIsForwarded(t *testing.T) {
	ts := newStubServer(t, testSessions()[:1], nil)
	cmd := buildTestSessionCmd(t, "")
	out, err := runCmd(t, cmd, "screen", "--json", "--server", ts.URL, "--token", testToken)
	if err != nil {
		t.Fatalf("screen --json: %s", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("screen --json did not emit JSON (%s):\n%s", err, out)
	}
	if payload["text"] != "hello from screen" {
		t.Errorf("forwarded payload = %v", payload)
	}
}

func TestSessionScreenPNGToFile(t *testing.T) {
	ts := newStubServer(t, testSessions()[:1], nil)
	target := filepath.Join(t.TempDir(), "shot.png")
	cmd := buildTestSessionCmd(t, "")
	out, err := runCmd(t, cmd, "screen", "--png", "-o", target, "--server", ts.URL, "--token", testToken)
	if err != nil {
		t.Fatalf("screen --png: %s", err)
	}
	if strings.TrimSpace(out) != target {
		t.Errorf("printed path = %q, want %q", strings.TrimSpace(out), target)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("reading the PNG: %s", err)
	}
	if !bytes.HasPrefix(data, []byte("\x89PNG")) {
		t.Errorf("written file is not a PNG: %q", data)
	}
}

func TestSessionScreenRejectsPNGWithJSON(t *testing.T) {
	cmd := buildTestSessionCmd(t, "")
	_, err := runCmd(t, cmd, "screen", "--png", "--json", "--server", "http://127.0.0.1:1", "--token", "t")
	if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Errorf("want a mutual-exclusion error, got %v", err)
	}
}

func TestSessionWaitSilentOnMatch(t *testing.T) {
	ts := newStubServer(t, testSessions()[:1], nil)
	cmd := buildTestSessionCmd(t, "")
	out, err := runCmd(t, cmd, "wait", "--text", "done", "--timeout", "1000", "--server", ts.URL, "--token", testToken, "--json=false")
	if err != nil {
		t.Fatalf("wait: %s", err)
	}
	if out != "" {
		t.Errorf("wait should be silent on success, wrote %q", out)
	}
	body := ts.last("wait")
	if body["regex"] != "done" {
		t.Errorf("posted regex = %v", body["regex"])
	}
	if body["timeout_ms"] != float64(1000) {
		t.Errorf("posted timeout_ms = %v, want 1000", body["timeout_ms"])
	}
}

func TestSessionWaitReportsTimeout(t *testing.T) {
	ts := newStubServer(t, testSessions()[:1], nil)
	ts.setWaitTimedOut(true)
	cmd := buildTestSessionCmd(t, "")
	_, err := runCmd(t, cmd, "wait", "--text", "never", "--timeout", "1000", "--server", ts.URL, "--token", testToken, "--json=false")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("want a timeout error, got %v", err)
	}
}

func TestSessionWaitValidatesArguments(t *testing.T) {
	cmd := buildTestSessionCmd(t, "")
	if _, err := runCmd(t, cmd, "wait", "--server", "http://127.0.0.1:1", "--token", "t"); err == nil {
		t.Error("want an error when neither --text nor --stable is given")
	}

	cmd = buildTestSessionCmd(t, "")
	_, err := runCmd(t, cmd, "wait", "--text", "x", "--timeout", "999999", "--server", "http://127.0.0.1:1", "--token", "t")
	if err == nil || !strings.Contains(err.Error(), "300000") {
		t.Errorf("want an upper-bound error, got %v", err)
	}
}

func TestSessionDestroyUsesResolvedID(t *testing.T) {
	ts := newStubServer(t, testSessions(), nil)
	cmd := buildTestSessionCmd(t, "")
	if _, err := runCmd(t, cmd, "destroy", "-s", "bbbb", "--server", ts.URL, "--token", testToken); err != nil {
		t.Fatalf("destroy: %s", err)
	}
	body := ts.last("destroy")
	if body["path"] != "/api/sessions/bbbbbbbbbbbbbbbb" {
		t.Errorf("deleted %v, want the prefix resolved to the full id", body["path"])
	}
}

func TestSessionSurfacesAuthFailures(t *testing.T) {
	ts := newStubServer(t, testSessions(), nil)
	cmd := buildTestSessionCmd(t, "")
	_, err := runCmd(t, cmd, "ls", "--server", ts.URL, "--token", "wrong-token", "--json=false")
	if err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("want an unauthorized error, got %v", err)
	}
	if !strings.Contains(err.Error(), "--token") {
		t.Errorf("the error should hint at --token, got %v", err)
	}
}

func TestRenderTablePadsColumnsWithoutTrailingSpace(t *testing.T) {
	out := renderTable([][]string{
		{"ID", "STATE"},
		{"a", "running"},
	})
	lines := strings.Split(out, "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2:\n%s", len(lines), out)
	}
	// The second column must start at the same offset on every row…
	header, row := strings.Index(lines[0], "STATE"), strings.Index(lines[1], "running")
	if header != row {
		t.Errorf("columns are misaligned (%d vs %d):\n%s", header, row, out)
	}
	// …and no row may carry trailing whitespace into piped output.
	for i, line := range lines {
		if strings.HasSuffix(line, " ") {
			t.Errorf("line %d has trailing whitespace: %q", i, line)
		}
	}
}

func TestFormatSessionsMarksExited(t *testing.T) {
	sessions := []session.StateDescription{{
		ID:     "aaaaaaaaaaaaaaaa",
		State:  "running",
		Exited: true,
		Spec:   session.ConnectSpec{HostID: "h_1", Name: "prod", Addr: "10.0.0.5:22", User: "root"},
	}}
	out := formatSessions(sessions)
	if !strings.Contains(out, "running (exited)") {
		t.Errorf("exited sessions should be marked:\n%s", out)
	}
}
