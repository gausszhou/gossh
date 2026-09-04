package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/gausszhou/gossh/internal/host"
	"github.com/gausszhou/gossh/internal/session"
	"github.com/gausszhou/gossh/internal/sshx"
	"github.com/gausszhou/gossh/internal/terminal"
)

const testToken = "test-token"

// stubTerminal implements session.Terminal with an in-memory pipe.
type stubTerminal struct {
	name string

	reader *io.PipeReader
	writer *io.PipeWriter

	mu       sync.Mutex
	written  []byte
	resizes  [][2]int
	signals  []syscall.Signal
	closedCh chan struct{}
}

func newStubTerminal(name string) *stubTerminal {
	r, w := io.Pipe()
	return &stubTerminal{name: name, reader: r, writer: w, closedCh: make(chan struct{})}
}

func (s *stubTerminal) Read(p []byte) (int, error) { return s.reader.Read(p) }
func (s *stubTerminal) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.written = append(s.written, p...)
	return len(p), nil
}
func (s *stubTerminal) Resize(cols, rows int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resizes = append(s.resizes, [2]int{cols, rows})
	return nil
}
func (s *stubTerminal) Signal(sig syscall.Signal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.signals = append(s.signals, sig)
	return nil
}
func (s *stubTerminal) Close() error {
	select {
	case <-s.closedCh:
	default:
		close(s.closedCh)
	}
	return s.reader.Close()
}
func (s *stubTerminal) Exited() bool {
	select {
	case <-s.closedCh:
		return true
	default:
		return false
	}
}
func (s *stubTerminal) Wait() error { <-s.closedCh; return nil }
func (s *stubTerminal) PipeWrite(data []byte) error {
	_, err := s.writer.Write(data)
	return err
}
func (s *stubTerminal) Command() string { return s.name }
func (s *stubTerminal) Args() []string  { return nil }
func (s *stubTerminal) PID() int        { return 0 }
func (s *stubTerminal) WindowTitleVariables() map[string]interface{} {
	return map[string]interface{}{"host": s.name}
}

// newTestServer builds a server with an in-memory inventory (one host h1)
// and a stub session factory (no real SSH dialing happens).
func newTestServer(t *testing.T, modify func(*Options)) (*httptest.Server, *session.Manager, *stubSink) {
	t.Helper()

	options := &Options{
		Address:        "127.0.0.1",
		Port:           "0",
		TitleFormat:    "test-title",
		PermitWrite:    true,
		Token:          testToken,
		TitleVariables: map[string]interface{}{"hostname": "testhost"},
	}
	if modify != nil {
		modify(options)
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
	kh, err := sshx.LoadKnownHosts(t.TempDir() + "/known_hosts")
	if err != nil {
		t.Fatalf("known hosts: %s", err)
	}
	secrets := sshx.NewSecrets()

	manager := session.NewManager()
	sink := &stubSink{}
	stubFactory := func(spec session.ConnectSpec, opts ...terminal.Option) (session.Terminal, error) {
		st := newStubTerminal(spec.Name)
		sink.last = st
		return st, nil
	}
	manager.WithTerminalFactory(stubFactory)

	ctx, cancel := context.WithCancel(context.Background())
	manager.Start(ctx)
	t.Cleanup(cancel)

	srv, err := New(manager, options, inv, kh, secrets)
	if err != nil {
		t.Fatalf("failed to create server: %s", err)
	}
	// 测试用 stub 工厂覆盖 api 注入的真拨号工厂
	manager.WithTerminalFactory(stubFactory)

	ts := httptest.NewServer(srv.setupHandlers())
	t.Cleanup(ts.Close)
	return ts, manager, sink
}

// doReq performs an authenticated HTTP request.
func doReq(t *testing.T, ts *httptest.Server, method, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, ts.URL+url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("failed to build request: %s", err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed request: %s", err)
	}
	return resp
}

func createSession(t *testing.T, ts *httptest.Server, body string) map[string]interface{} {
	t.Helper()
	resp := doReq(t, ts, http.MethodPost, "/api/sessions", body)
	defer resp.Body.Close()
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %s", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("unexpected status: %d, body: %v", resp.StatusCode, result)
	}
	id, _ := result["id"].(string)
	if id == "" {
		t.Fatal("session id must not be empty")
	}
	return result
}

func createSessionWithID(t *testing.T, ts *httptest.Server, id, body string, expectedStatus int) map[string]interface{} {
	t.Helper()
	// 把 id 并入请求体(与 gotty 原版一致:显式客户端 id 由 body 携带)
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		payload = map[string]interface{}{}
	}
	payload["id"] = id
	withID, _ := json.Marshal(payload)
	resp := doReq(t, ts, http.MethodPost, "/api/sessions", string(withID))
	defer resp.Body.Close()
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %s", err)
	}
	if resp.StatusCode != expectedStatus {
		t.Fatalf("unexpected status: %d (want %d), body: %v", resp.StatusCode, expectedStatus, result)
	}
	return result
}

func postStatus(t *testing.T, ts *httptest.Server, ids []string) sessionStatusResponse {
	t.Helper()
	body, _ := json.Marshal(map[string]interface{}{"ids": ids})
	resp := doReq(t, ts, http.MethodPost, "/api/sessions/status", string(body))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status code: %d", resp.StatusCode)
	}
	var out sessionStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("failed to decode status: %s", err)
	}
	return out
}

func TestRESTLifecycle(t *testing.T) {
	ts, _, _ := newTestServer(t, nil)

	// create by host
	created := createSession(t, ts, `{"host_id":"h1"}`)
	id := created["id"].(string)
	if created["state"] != "idle" {
		t.Fatalf("unexpected state: %v", created["state"])
	}

	status := postStatus(t, ts, []string{id, "zzzzzzzzzzzzzzzz"})
	if _, ok := status.Sessions[id]; !ok {
		t.Fatal("created session missing from status")
	}
	if _, ok := status.Sessions["zzzzzzzzzzzzzzzz"]; ok {
		t.Fatal("unknown session must not be reported alive")
	}

	resp := doReq(t, ts, http.MethodGet, "/api/sessions/"+id, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected get status: %d", resp.StatusCode)
	}

	resp = doReq(t, ts, http.MethodGet, "/api/sessions/does-not-exist", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unexpected status for missing session: %d", resp.StatusCode)
	}

	resp = doReq(t, ts, http.MethodPost, "/api/sessions/"+id+"/resize", `{"width":100,"height":30}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected resize status: %d", resp.StatusCode)
	}

	resp = doReq(t, ts, http.MethodPost, "/api/sessions/"+id+"/signal", `{"signal":"SIGKILL"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected signal status: %d", resp.StatusCode)
	}
	resp = doReq(t, ts, http.MethodPost, "/api/sessions/"+id+"/signal", `{"signal":"SIGBOGUS"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unexpected status for unknown signal: %d", resp.StatusCode)
	}

	resp = doReq(t, ts, http.MethodDelete, "/api/sessions/"+id, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("unexpected delete status: %d", resp.StatusCode)
	}

	resp = doReq(t, ts, http.MethodGet, "/api/sessions/"+id, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted session must be gone, status: %d", resp.StatusCode)
	}
}

func TestRESTCreateRequiresHost(t *testing.T) {
	ts, _, _ := newTestServer(t, nil)

	resp := doReq(t, ts, http.MethodPost, "/api/sessions", `{}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 without host, got: %d", resp.StatusCode)
	}

	resp = doReq(t, ts, http.MethodPost, "/api/sessions", `{"host_id":"nope"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 with unknown host, got: %d", resp.StatusCode)
	}
}

func TestTokenRequired(t *testing.T) {
	ts, _, _ := newTestServer(t, nil)

	// 无令牌 → 401
	resp, err := http.Get(ts.URL + "/api/hosts")
	if err != nil {
		t.Fatalf("request failed: %s", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got: %d", resp.StatusCode)
	}

	// ?token= 查询参数同样有效,且不要求鉴权头
	resp, err = http.Get(ts.URL + "/api/hosts?token=" + testToken)
	if err != nil {
		t.Fatalf("request failed: %s", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with token in query, got: %d", resp.StatusCode)
	}
}

func TestSiteEndpoints(t *testing.T) {
	ts, _, _ := newTestServer(t, nil)

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("index request failed: %s", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected index status: %d", resp.StatusCode)
	}
}

func TestHostsCRUD(t *testing.T) {
	ts, _, _ := newTestServer(t, nil)

	// create
	resp := doReq(t, ts, http.MethodPost, "/api/hosts", `{"name":"web","address":"10.0.0.5","user":"deploy"}`)
	var created *host.Host
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %s", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated || created.ID == "" {
		t.Fatalf("unexpected create result: %d %+v", resp.StatusCode, created)
	}

	// list
	resp = doReq(t, ts, http.MethodGet, "/api/hosts", "")
	var list []*host.Host
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatalf("decode: %s", err)
	}
	resp.Body.Close()
	if len(list) != 2 { // h1 (from newTestServer) + web
		t.Fatalf("expected 2 hosts, got %d", len(list))
	}

	// update
	resp = doReq(t, ts, http.MethodPut, "/api/hosts/"+created.ID, `{"name":"web2","address":"10.0.0.6","user":"deploy"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected update status: %d", resp.StatusCode)
	}

	// delete
	resp = doReq(t, ts, http.MethodDelete, "/api/hosts/"+created.ID, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("unexpected delete status: %d", resp.StatusCode)
	}
}

func TestWSAttachE2E(t *testing.T) {
	ts, _, sink := newTestServer(t, nil)

	created := createSession(t, ts, `{"host_id":"h1"}`)
	id := created["id"].(string)
	if err := sink.last.PipeWrite([]byte("hello\r\n")); err != nil {
		t.Fatalf("feed failed: %s", err)
	}

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?session_id=" + id + "&token=" + testToken
	conn, _, err := websocket.Dial(context.Background(), wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial ws: %s", err)
	}
	defer conn.CloseNow()

	// 读握手帧:WindowTitle → (Output 重放) → SetReplayDone
	deadline := time.After(3 * time.Second)
	sawOutput := false
	for {
		_, data, err := conn.Read(context.Background())
		if err != nil {
			t.Fatalf("failed to read ws frame: %s", err)
		}
		if len(data) > 0 && data[0] == terminal.Output {
			sawOutput = true
		}
		if sawOutput && len(data) > 0 && data[0] == terminal.SetReplayDone {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timeout waiting for attach handshake")
		default:
		}
	}

	// 输入帧
	if err := conn.Write(context.Background(), websocket.MessageBinary, []byte{terminal.Input, 'h', 'i'}); err != nil {
		t.Fatalf("failed to write input: %s", err)
	}
	// ping → pong
	if err := conn.Write(context.Background(), websocket.MessageBinary, []byte{terminal.Ping}); err != nil {
		t.Fatalf("failed to write ping: %s", err)
	}
	conn.SetReadLimit(1 << 20)
	_, data, err := conn.Read(context.Background())
	if err != nil {
		t.Fatalf("failed to read pong: %s", err)
	}
	if len(data) != 1 || data[0] != terminal.Pong {
		t.Fatalf("expected pong frame, got %v", data)
	}
}

func TestWSPreemptsSession(t *testing.T) {
	ts, _, sink := newTestServer(t, nil)
	created := createSession(t, ts, `{"host_id":"h1"}`)
	id := created["id"].(string)
	if err := sink.last.PipeWrite([]byte("hello\r\n")); err != nil {
		t.Fatalf("feed failed: %s", err)
	}

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?session_id=" + id + "&token=" + testToken
	c1, _, err := websocket.Dial(context.Background(), wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial c1: %s", err)
	}
	defer c1.CloseNow()
	c1.SetReadLimit(1 << 20)

	c2, _, err := websocket.Dial(context.Background(), wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial c2: %s", err)
	}
	defer c2.CloseNow()
	c2.SetReadLimit(1 << 20)

	// c1 应因抢占而关闭(1013):读操作应立即结算(返回数据或错误都算)
	closed := make(chan struct{})
	go func() {
		for {
			_, _, err := c1.Read(context.Background())
			if err != nil {
				close(closed)
				return
			}
			// 可能还有排队的握手帧;继续读直到出错(被抢占关闭)
		}
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("c1 not closed by preemption")
	}
}

func TestCreateWithClientIDIdempotent(t *testing.T) {
	ts, _, _ := newTestServer(t, nil)
	const id = "aaaaaaaaaaaaaaaa"

	createSessionWithID(t, ts, id, `{"host_id":"h1"}`, http.StatusCreated)
	again := createSessionWithID(t, ts, id, `{"host_id":"h1"}`, http.StatusOK)
	if again["id"] != id {
		t.Fatalf("idempotent hit must keep the id, got %v", again["id"])
	}
}

func TestCreateWithClientIDRejectsBadFormat(t *testing.T) {
	ts, _, _ := newTestServer(t, nil)
	resp := doReq(t, ts, http.MethodPost, "/api/sessions", `{"id":"BAD","host_id":"h1"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad id format, got %d", resp.StatusCode)
	}
}

func TestSessionStatusBatch(t *testing.T) {
	ts, _, _ := newTestServer(t, nil)
	id1 := createSession(t, ts, `{"host_id":"h1"}`)["id"].(string)
	id2 := createSession(t, ts, `{"host_id":"h1"}`)["id"].(string)

	status := postStatus(t, ts, []string{id1, id2, "nope"})
	if len(status.Sessions) != 2 {
		t.Fatalf("expected 2 alive, got %d", len(status.Sessions))
	}
	if status.Sessions[id1].Spec.HostID != "h1" {
		t.Fatalf("spec missing: %+v", status.Sessions[id1])
	}
}

func TestPageTitleRoundTrip(t *testing.T) {
	ts, _, _ := newTestServer(t, nil)

	resp := doReq(t, ts, http.MethodPut, "/api/title", `{"title":"My SSH"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected put status: %d", resp.StatusCode)
	}

	resp = doReq(t, ts, http.MethodGet, "/api/title", "")
	defer resp.Body.Close()
	var out map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %s", err)
	}
	if out["title"] != "My SSH" {
		t.Fatalf("unexpected title: %v", out)
	}
}
