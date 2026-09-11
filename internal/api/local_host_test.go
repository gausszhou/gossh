package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/gausszhou/gossh/internal/host"
	"github.com/gausszhou/gossh/internal/session"
	"github.com/gausszhou/gossh/internal/sshx"
)

// TestLocalServerListedFirstAndReadOnly 内置本地服务器:列表首位常驻、
// 可连接,但不可编辑/删除(它不是主机清单记录,见 ADR-0008)。
func TestLocalServerListedFirstAndReadOnly(t *testing.T) {
	ts, _, _ := newTestServer(t, nil)

	resp := doReq(t, ts, http.MethodGet, "/api/hosts", "")
	var list []*host.Host
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatalf("decode hosts: %s", err)
	}
	resp.Body.Close()
	if len(list) == 0 {
		t.Fatal("GET /api/hosts returned nothing")
	}
	if !host.IsLocal(list[0].ID) || !list[0].Builtin {
		t.Fatalf("expected the built-in local server first, got %+v", list[0])
	}
	if list[0].Address != "127.0.0.1" || list[0].User == "" {
		t.Fatalf("local server record lacks display fields: %+v", list[0])
	}

	// 单条查询同样能解析内置 id
	resp = doReq(t, ts, http.MethodGet, "/api/hosts/"+host.LocalID, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/hosts/%s = %d, want 200", host.LocalID, resp.StatusCode)
	}
	resp.Body.Close()

	// 编辑/删除都必须被拒绝(否则内置条目会被写进 hosts.json 或删掉)
	resp = doReq(t, ts, http.MethodPut, "/api/hosts/"+host.LocalID,
		`{"name":"hijack","address":"10.0.0.9","user":"root"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("PUT /api/hosts/%s = %d, want 400", host.LocalID, resp.StatusCode)
	}
	resp.Body.Close()

	resp = doReq(t, ts, http.MethodDelete, "/api/hosts/"+host.LocalID, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("DELETE /api/hosts/%s = %d, want 400", host.LocalID, resp.StatusCode)
	}
	resp.Body.Close()
}

// TestCreateLocalSessionSpec 用 host_id=local 建会话:API 走的是与真实主机
// 完全相同的 host id → ConnectSpec 路径,spec 携带本地记录(address/user
// 仅用于展示,会话工厂按 HostID 分派到本地 PTY)。
func TestCreateLocalSessionSpec(t *testing.T) {
	ts, _, sink := newTestServer(t, nil)

	resp := doReq(t, ts, http.MethodPost, "/api/sessions", `{"host_id":"`+host.LocalID+`"}`)
	var desc session.StateDescription
	if err := json.NewDecoder(resp.Body).Decode(&desc); err != nil {
		t.Fatalf("decode session: %s", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create local session = %d, want 201", resp.StatusCode)
	}
	if desc.Spec.HostID != host.LocalID {
		t.Fatalf("spec host id = %q, want %q", desc.Spec.HostID, host.LocalID)
	}
	if desc.Spec.Addr != "127.0.0.1:22" || desc.Spec.User == "" {
		t.Fatalf("spec lacks the local display fields: %+v", desc.Spec)
	}
	// 工厂确实被调用(测试里是 stub 工厂,真拨号工厂会按 LocalID 走本地 PTY)
	if sink.last == nil {
		t.Fatal("terminal factory was never called for the local session")
	}
}

// TestDialHostForwardRejectsLocalServer 主机级转发的拨号入口对本地服务器
// 必须直接拒绝:本地服务器是本机终端,没有 SSH 连接可承载转发,放过去
// 会变成对 127.0.0.1:22 的真实拨号尝试。
func TestDialHostForwardRejectsLocalServer(t *testing.T) {
	inv, err := host.LoadInventory(t.TempDir() + "/hosts.json")
	if err != nil {
		t.Fatalf("inventory: %s", err)
	}
	kh, err := sshx.LoadKnownHosts(t.TempDir() + "/known_hosts")
	if err != nil {
		t.Fatalf("known hosts: %s", err)
	}
	srv, err := New(session.NewManager(), &Options{
		Address:     "127.0.0.1",
		Port:        "0",
		TitleFormat: "test-title",
		Token:       testToken,
	}, inv, kh, sshx.NewSecrets())
	if err != nil {
		t.Fatalf("failed to create server: %s", err)
	}

	if _, err := srv.dialHostForward(host.LocalID, nil); !errors.Is(err, host.ErrBuiltin) {
		t.Fatalf("dialHostForward(local) = %v, want ErrBuiltin", err)
	}
}
