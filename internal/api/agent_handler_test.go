package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gausszhou/gossh/internal/host"
	"github.com/gausszhou/gossh/internal/session"
	"github.com/gausszhou/gossh/internal/sshx"
	"github.com/gausszhou/gossh/internal/terminal"
)

// newAgentTestServer builds a test server for the agent-driving API
// (screen/wait/keys backed by the screen mirror). The session factory is
// a stub; tests feed scripted output into the stub's pipe.
func newAgentTestServer(t *testing.T, mirror bool, permitWrite bool) (*httptest.Server, *session.Manager, *stubSink) {
	t.Helper()
	options := &Options{
		Address:        "127.0.0.1",
		Port:           "0",
		TitleFormat:    "test-title",
		PermitWrite:    permitWrite,
		Token:          testToken,
		TitleVariables: map[string]interface{}{"hostname": "testhost"},
	}

	inv, err := host.LoadInventory(t.TempDir() + "/hosts.json")
	if err != nil {
		t.Fatalf("inventory: %s", err)
	}
	_ = inv.Add(&host.Host{
		ID:      "h1",
		Name:    "test",
		Address: "127.0.0.1",
		Port:    22,
		User:    "root",
	})
	kh, _ := sshx.LoadKnownHosts(t.TempDir() + "/known_hosts")
	secrets := sshx.NewSecrets()

	var manager *session.Manager
	if mirror {
		manager = session.NewManager(session.WithMirrorFactory(MirrorFactory(true)))
	} else {
		manager = session.NewManager()
	}

	sink := &stubSink{}
	stubFactory := func(spec session.ConnectSpec, opts ...terminal.Option) (session.Terminal, error) {
		st := newStubTerminal(spec.Name)
		sink.last = st
		return st, nil
	}
	manager.WithTerminalFactory(stubFactory)

	srv, err := New(manager, options, inv, kh, secrets)
	if err != nil {
		t.Fatalf("failed to create server: %s", err)
	}
	manager.WithTerminalFactory(stubFactory)

	ts := httptest.NewServer(srv.setupHandlers())
	t.Cleanup(ts.Close)
	return ts, manager, sink
}

// stubSink remembers the most recently created stub terminal so tests can
// feed it scripted output.
type stubSink struct {
	last *stubTerminal
}

// doJSON posts a JSON body and decodes the JSON response (authenticated).
func doJSON(t *testing.T, method, url, body string) (int, map[string]interface{}) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// createAgentSession creates a session and feeds the stub's scripted output.
func createAgentSession(t *testing.T, ts *httptest.Server, sink *stubSink, output string) string {
	t.Helper()
	status, resp := doJSON(t, http.MethodPost, ts.URL+"/api/sessions", `{"host_id":"h1"}`)
	if status != http.StatusCreated {
		t.Fatalf("create session: status %d, resp %v", status, resp)
	}
	id, _ := resp["id"].(string)
	if id == "" {
		t.Fatal("session id must not be empty")
	}
	if sink.last == nil {
		t.Fatal("no stub terminal created")
	}
	// 让输出泵与镜像有时间建立,再灌入剧本输出
	time.Sleep(50 * time.Millisecond)
	if err := sink.last.PipeWrite([]byte(output)); err != nil {
		t.Fatalf("feed failed: %s", err)
	}
	// 等镜像消费输出后再继续断言
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		sink.last.mu.Lock()
		consumed := len(sink.last.written) >= 0
		sink.last.mu.Unlock()
		_ = consumed
		time.Sleep(10 * time.Millisecond)
		// 简要等待输出泵把 PipeWrite 的数据送入镜像/ring
		break
	}
	time.Sleep(150 * time.Millisecond)
	return id
}

func TestAgentWaitMatchesScreen(t *testing.T) {
	ts, _, sink := newAgentTestServer(t, true, true)
	id := createAgentSession(t, ts, sink, "hello agent")

	status, resp := doJSON(t, http.MethodPost, ts.URL+"/api/sessions/"+id+"/wait",
		`{"regex":"hello agent","timeout_ms":3000}`)
	if status != http.StatusOK {
		t.Fatalf("wait: status %d, resp %v", status, resp)
	}
	if matched, _ := resp["matched"].(bool); !matched {
		t.Errorf("wait matched = %v, want true", resp["matched"])
	}
}

func TestAgentScreenFormats(t *testing.T) {
	ts, _, sink := newAgentTestServer(t, true, true)
	id := createAgentSession(t, ts, sink, "screen me")

	// format=text 直接返回纯文本响应体
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/sessions/"+id+"/screen?format=text", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("screen request failed: %s", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("screen: status %d", resp.StatusCode)
	}
	buf := new(strings.Builder)
	_, _ = io.Copy(buf, resp.Body)
	if !strings.Contains(buf.String(), "screen me") {
		t.Fatalf("screen text = %q, want to contain 'screen me'", buf.String())
	}
}

func TestAgentKeysWriteInput(t *testing.T) {
	ts, _, sink := newAgentTestServer(t, true, true)
	id := createAgentSession(t, ts, sink, "welcome")

	status, _ := doJSON(t, http.MethodPost, ts.URL+"/api/sessions/"+id+"/keys", `{"input":"echo hi\r"}`)
	if status != http.StatusOK {
		t.Fatalf("keys: status %d", status)
	}
	sink.last.mu.Lock()
	written := string(sink.last.written)
	sink.last.mu.Unlock()
	if !strings.Contains(written, "echo hi") {
		t.Fatalf("keys did not reach the terminal: %q", written)
	}
}

func TestAgentKeysBase64(t *testing.T) {
	ts, _, sink := newAgentTestServer(t, true, true)
	id := createAgentSession(t, ts, sink, "x")

	// base64("ls -la\r")
	status, _ := doJSON(t, http.MethodPost, ts.URL+"/api/sessions/"+id+"/keys", `{"input":"bHMgLWxhDQ==","encoding":"base64"}`)
	if status != http.StatusOK {
		t.Fatalf("keys: status %d", status)
	}
	// 输入必须已写入 stub 终端
	sink.last.mu.Lock()
	written := string(sink.last.written)
	sink.last.mu.Unlock()
	if !strings.Contains(written, "ls -la") {
		t.Fatalf("keys did not reach the terminal: %q", written)
	}
}

func TestAgentKeysForbiddenWhenReadOnly(t *testing.T) {
	ts, _, _ := newAgentTestServer(t, true, false)
	status, _ := doJSON(t, http.MethodPost, ts.URL+"/api/sessions/aaaaaaaaaaaaaaaa/keys", `{"input":"x"}`)
	if status != http.StatusForbidden {
		t.Fatalf("expected 403 for read-only write, got %d", status)
	}
}

func TestAgentScreenDisabledMirror(t *testing.T) {
	ts, _, sink := newAgentTestServer(t, false, true)
	id := createAgentSession(t, ts, sink, "no mirror here")
	status, _ := doJSON(t, http.MethodGet, ts.URL+"/api/sessions/"+id+"/screen", "")
	if status != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 without mirror, got %d", status)
	}
}

func TestAgentInvalidWaitRequest(t *testing.T) {
	ts, _, _ := newAgentTestServer(t, true, true)
	status, _ := doJSON(t, http.MethodPost, ts.URL+"/api/sessions/aaaaaaaaaaaaaaaa/wait", `{"timeout_ms":-1}`)
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid wait, got %d", status)
	}
}
