package api

import (
	"log"
	"net/http"
	"sync"

	"github.com/gausszhou/gossh/internal/host"
	"github.com/gausszhou/gossh/internal/sshx"

	"golang.org/x/crypto/ssh"
)

// 主机级端口转发常驻(ADR-0007, Step A:解耦)。
//
// 历史模型:主机记录里的 forwards 在每次会话建立时编程到该会话的连接上,
// 会话销毁即关闭——转发依赖会话。本文件把主机级转发提升为 per-host 的
// 运行时对象:每台主机最多一条「转发连接」(复用 sshx 连接链拨号与凭据
// 解析,与会话拨号同一套),转发挂在它上面,不随任何会话生灭;同主机多
// 个页签共享同一组转发,不再端口冲突。
//
// 凭据策略:key/钥匙串凭据可无头拨号;仅存在于浏览器的密码,由该主机
// 任一会话建立时 (handleCreateSession → ensure) 顺带提供,服务端以相同
// 凭据另拨一条转发连接(A-1「借凭据再拨一条」,不移交会话连接)。
//
// 边界(属 Step B,不在本步):断线自动重连、服务重启自动恢复、UI 开关。
// 连接断线后转发停止,状态标 failed;下次会话建立或主机配置变更会重拨。

// HostForward 是单条主机级转发的运行时视图(挂在主机转发连接上)。
type HostForward struct {
	ID     string      `json:"id"`
	Kind   ForwardKind `json:"kind"`
	Bind   string      `json:"bind"`
	Target string      `json:"target"`
	Status string      `json:"status"` // running | pending | failed
	Error  string      `json:"error,omitempty"`
}

// ForwardHostState 是单台主机的转发运行时:一条独立转发连接 + 其上的活跃转发。
type ForwardHostState struct {
	hostID string
	mu     sync.Mutex
	dial   *sshx.DialResult
	// entries 正在运行的转发,key = forwardKey(kind|bind|target)
	entries map[string]*ForwardEntry
	// failed 启动失败的转发,key 同上,value 为错误信息(仍参与列示)
	failed  map[string]string
	lastErr string
}

// dialHostFunc 建立主机级转发连接:解析连接链 + TOFU + 凭据,与会话拨号
// 同一条链路(见 Server.dialHostForward)。
type dialHostFunc func(hostID string, prov *sshx.ProvidedSecrets) (*sshx.DialResult, error)

// launchForwardFunc 在一条 ssh 连接上启动一个转发(Server.launchOnClient)。
type launchForwardFunc func(client *ssh.Client, kind ForwardKind, bind, target string) (*ForwardEntry, error)

// hostForwardSpecFunc 返回主机的转发配置(host.Forwards,来源 hosts.json)。
type hostForwardSpecFunc func(hostID string) []host.Forward

// ForwardHostManager 管理各主机的转发连接与转发,key = hostID。
type ForwardHostManager struct {
	mu     sync.Mutex
	states map[string]*ForwardHostState

	dial   dialHostFunc
	launch launchForwardFunc
	specs  hostForwardSpecFunc
}

// NewForwardHostManager 构造转发主机管理器。
func NewForwardHostManager(dial dialHostFunc, launch launchForwardFunc, specs hostForwardSpecFunc) *ForwardHostManager {
	return &ForwardHostManager{
		states: map[string]*ForwardHostState{},
		dial:   dial,
		launch: launch,
		specs:  specs,
	}
}

func forwardKey(f host.Forward) string { return string(f.Kind) + "|" + f.Bind + "|" + f.Target }

// state 返回(必要时创建)主机的转发状态。
func (m *ForwardHostManager) state(hostID string) *ForwardHostState {
	m.mu.Lock()
	defer m.mu.Unlock()
	if st, ok := m.states[hostID]; ok {
		return st
	}
	st := &ForwardHostState{
		hostID:  hostID,
		entries: map[string]*ForwardEntry{},
		failed:  map[string]string{},
	}
	m.states[hostID] = st
	return st
}

// ensure 让主机的转发就绪(幂等):已有健康连接且转发在跑则直接返回;
// 否则带凭据拨号并启动 host.Forwards。拨号失败置 failed,下次调用重试。
// 由会话建立时调用(浏览器密码顺带成为转发连接的拨号凭据)。
func (m *ForwardHostManager) ensure(hostID string, prov *sshx.ProvidedSecrets) {
	st := m.state(hostID)
	st.mu.Lock()
	defer st.mu.Unlock()

	if st.dial != nil {
		// 连接还在:补跑配置变更(主机更新也会走 reconcile,这里是兜底)
		m.reconcileLocked(st)
		return
	}

	dial, err := m.dial(hostID, prov)
	if err != nil {
		st.lastErr = err.Error()
		log.Printf("Host forward connection failed for %s: %s", hostID, err)
		return
	}
	st.dial = dial
	st.lastErr = ""
	log.Printf("Host forward connection established for %s", hostID)
	m.reconcileLocked(st)
}

// reconcile 按主机当前配置 diff 启动/撤销转发(host.Forwards 变更后调用)。
func (m *ForwardHostManager) reconcile(hostID string) {
	st := m.state(hostID)
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.dial == nil {
		// 转发连接尚未建立(如仅 keyring 无会话时),等下次 ensure 一起拉起
		return
	}
	m.reconcileLocked(st)
}

// reconcileLocked 在持有 st.mu 的情况下同步配置与运行态。
func (m *ForwardHostManager) reconcileLocked(st *ForwardHostState) {
	want := m.specs(st.hostID)
	wanted := map[string]host.Forward{}
	for _, f := range want {
		wanted[forwardKey(f)] = f
	}

	// 撤销:配置里已不存在(或已换绑端口)的转发
	for key, entry := range st.entries {
		if _, ok := wanted[key]; ok {
			continue
		}
		if entry.cancel != nil {
			entry.cancel()
		}
		delete(st.entries, key)
		delete(st.failed, key)
		log.Printf("Host forward removed on %s: %s", st.hostID, key)
	}

	// 启动/重试:配置存在但未运行的转发
	for _, f := range want {
		key := forwardKey(f)
		if _, running := st.entries[key]; running {
			continue
		}
		entry, err := m.launch(st.dial.Target, ForwardKind(f.Kind), f.Bind, f.Target)
		if err != nil {
			st.failed[key] = err.Error()
			log.Printf("Host forward failed on %s: %s %s -> %s: %s", st.hostID, f.Kind, f.Bind, f.Target, err)
			continue
		}
		st.entries[key] = entry
		delete(st.failed, key)
		log.Printf("Host forward applied on %s: %s %s -> %s", st.hostID, f.Kind, f.Bind, f.Target)
	}
}

// release 停止并释放主机的转发连接与所有转发(主机删除 / 服务关闭时调用)。
func (m *ForwardHostManager) release(hostID string) {
	m.mu.Lock()
	st, ok := m.states[hostID]
	delete(m.states, hostID)
	m.mu.Unlock()
	if !ok {
		return
	}

	st.mu.Lock()
	defer st.mu.Unlock()
	for _, entry := range st.entries {
		if entry.cancel != nil {
			entry.cancel()
		}
	}
	st.entries = map[string]*ForwardEntry{}
	st.failed = map[string]string{}
	if st.dial != nil {
		_ = st.dial.Close()
	}
	st.dial = nil
	log.Printf("Host forward connection released for %s", hostID)
}

// closeAll 释放全部主机的转发连接(服务退出)。
func (m *ForwardHostManager) closeAll() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.states))
	for id := range m.states {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.release(id)
	}
}

// list 返回主机的转发运行时视图(供 GET /api/hosts/{id}/forwards)。
func (m *ForwardHostManager) list(hostID string) []HostForward {
	st := m.state(hostID)
	st.mu.Lock()
	defer st.mu.Unlock()

	out := make([]HostForward, 0, len(m.specs(hostID)))
	for _, f := range m.specs(hostID) {
		hf := HostForward{
			Kind:   ForwardKind(f.Kind),
			Bind:   f.Bind,
			Target: f.Target,
		}
		key := forwardKey(f)
		if st.dial == nil {
			if st.lastErr != "" {
				hf.Status = "failed"
				hf.Error = st.lastErr
			} else {
				hf.Status = "pending" // 尚未建立转发连接(等下次会话/凭据)
			}
		} else if entry, ok := st.entries[key]; ok {
			hf.Status = "running"
			hf.ID = entry.ID
		} else if msg, ok := st.failed[key]; ok {
			hf.Status = "failed"
			hf.Error = msg
		} else {
			// 连接在但该转发未启动(如配置刚改、等待 reconcile)
			hf.Status = "running"
		}
		out = append(out, hf)
	}
	return out
}

// handleListHostForwards implements GET /api/hosts/{id}/forwards.
func (server *Server) handleListHostForwards(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, server.forwardHosts.list(r.PathValue("id")))
}
