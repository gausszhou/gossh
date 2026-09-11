package api

import (
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gausszhou/gossh/internal/host"
	"github.com/gausszhou/gossh/internal/sshx"

	"golang.org/x/crypto/ssh"
)

// 主机级端口转发常驻(ADR-0007, Step A 解耦 + Step B 常驻服务化)。
//
// 历史模型:主机记录里的 forwards 在每次会话建立时编程到该会话的连接上,
// 会话销毁即关闭——转发依赖会话。本文件把主机级转发提升为 per-host 的
// 运行时对象:每台主机最多一条「转发连接」(复用 sshx 连接链拨号与凭据
// 解析,与会话拨号同一套),转发挂在它上面,不随任何会话生灭;同主机多
// 个页签共享同一组转发,不再端口冲突。
//
// 凭据策略:key/钥匙串凭据可无头拨号;仅存在于浏览器的密码,由该主机
// 任一会话建立时 (handleCreateSession → ensure) 顺带提供,服务端以相同
// 凭据另拨一条转发连接(A-1「借凭据再拨一条」,不移交会话连接)。最近
// 一次成功拨号的凭据只留在内存(lastProv),断线重连时复用,绝不持久化。
//
// Step B(常驻服务化):每台主机的转发连接由后台 supervise goroutine
// 看管——连接断开自动以指数退避重连并重拉转发;服务启动时 resumeAll
// 恢复所有配置了启用的转发的主机;单条转发可停用(Forward.Enabled)。

// HostForward 是单条主机级转发的运行时视图(挂在主机转发连接上)。
type HostForward struct {
	ID     string      `json:"id"`
	Kind   ForwardKind `json:"kind"`
	Bind   string      `json:"bind"`
	Target string      `json:"target"`
	Status string      `json:"status"` // running | pending | failed | disabled
	Error  string      `json:"error,omitempty"`
}

// ForwardHostState 是单台主机的转发运行时:一条独立转发连接 + 其上的活跃转发。
type ForwardHostState struct {
	hostID string
	mu     sync.Mutex
	dial   *sshx.DialResult
	// lastProv 最近一次成功拨号所用的凭据(仅内存,供断线重连复用)。
	lastProv *sshx.ProvidedSecrets
	// entries 正在运行的转发,key = forwardKey(kind|bind|target)
	entries map[string]*ForwardEntry
	// failed 启动失败的转发,key 同上,value 为错误信息(仍参与列示)
	failed  map[string]string
	lastErr string
	// stop 关闭即要求 supervise goroutine 退出(release/closeAll 时)。
	stop chan struct{}
	// supervising 标记后台看管 goroutine 是否在跑(每份 state 最多一个)。
	supervising bool
}

// dialHostFunc 建立主机级转发连接:解析连接链 + TOFU + 凭据,与会话拨号
// 同一条链路(见 Server.dialHostForward)。
type dialHostFunc func(hostID string, prov *sshx.ProvidedSecrets) (*sshx.DialResult, error)

// launchForwardFunc 在一条 ssh 连接上启动一个转发(Server.launchOnClient)。
type launchForwardFunc func(client *ssh.Client, kind ForwardKind, bind, target string) (*ForwardEntry, error)

// hostForwardSpecFunc 返回主机的转发配置(host.Forwards,来源 hosts.json)。
type hostForwardSpecFunc func(hostID string) []host.Forward

// hostListFunc 返回全部主机 ID(启动恢复用)。
type hostListFunc func() []string

// connDoneFunc 返回一个在连接死亡时关闭的 channel。生产实现基于
// ssh.Client.Wait;测试注入假信号,不必真起 SSH 连接。
type connDoneFunc func(*sshx.DialResult) <-chan struct{}

// defaultConnDone 用 ssh.Client.Wait 感知连接断开。Target 为 nil(测试桩
// 的假连接)时返回永不关闭的 channel——观测不到死亡就当作健在,绝不能
// 误判成「立即死亡」。
func defaultConnDone(dr *sshx.DialResult) <-chan struct{} {
	ch := make(chan struct{})
	if dr == nil || dr.Target == nil {
		return ch
	}
	go func() {
		_ = dr.Target.Wait()
		close(ch)
	}()
	return ch
}

// ForwardHostManager 管理各主机的转发连接与转发,key = hostID。
type ForwardHostManager struct {
	mu     sync.Mutex
	states map[string]*ForwardHostState

	dial   dialHostFunc
	launch launchForwardFunc
	specs  hostForwardSpecFunc
	hosts  hostListFunc

	// connDone 连接死亡信号源;nil 用 ssh.Client.Wait 实现。
	connDone connDoneFunc
	// 重连退避:base 起步逐次翻倍,封顶 max。零值取默认(1s / 30s)。
	baseBackoff time.Duration
	maxBackoff  time.Duration
}

// NewForwardHostManager 构造转发主机管理器。
func NewForwardHostManager(dial dialHostFunc, launch launchForwardFunc, specs hostForwardSpecFunc, hosts hostListFunc) *ForwardHostManager {
	return &ForwardHostManager{
		states: map[string]*ForwardHostState{},
		dial:   dial,
		launch: launch,
		specs:  specs,
		hosts:  hosts,
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
		stop:    make(chan struct{}),
	}
	m.states[hostID] = st
	return st
}

// hasEnabledForwards 报告主机是否配置了至少一条启用的转发。
func (m *ForwardHostManager) hasEnabledForwards(hostID string) bool {
	for _, f := range m.specs(hostID) {
		if f.IsEnabled() {
			return true
		}
	}
	return false
}

// resume 保证主机的转发连接被后台看管(幂等):启动 supervise goroutine。
// prov 非空时记住它,供断线重连复用。没有启用中的转发的主机不看管。
func (m *ForwardHostManager) resume(hostID string, prov *sshx.ProvidedSecrets) {
	if prov != nil {
		st := m.state(hostID)
		st.mu.Lock()
		st.lastProv = prov
		st.mu.Unlock()
	}
	st := m.state(hostID)
	st.mu.Lock()
	if st.supervising || !m.hasEnabledForwards(hostID) {
		st.mu.Unlock()
		return
	}
	st.supervising = true
	st.mu.Unlock()
	log.Printf("Host forward supervision started for %s", hostID)
	go m.supervise(st)
}

// resumeAll 恢复所有配置了启用转发的主机(服务启动时调用,ADR-0007 Step B)。
// 无头凭据(key/agent/keyring)的主机立即拨号;仅浏览器密码的主机保持
// pending,等第一次会话建立时借凭据。
func (m *ForwardHostManager) resumeAll() {
	if m.hosts == nil {
		return
	}
	for _, id := range m.hosts() {
		if host.IsLocal(id) {
			continue
		}
		if !m.hasEnabledForwards(id) {
			continue
		}
		m.resume(id, nil)
	}
}

// supervise 后台看管一台主机的转发连接:没连接就拨号(指数退避),连接
// 断开就清理转发并重连。退出条件只有 stop(release/closeAll)。
func (m *ForwardHostManager) supervise(st *ForwardHostState) {
	base := m.baseBackoff
	if base <= 0 {
		base = time.Second
	}
	maxB := m.maxBackoff
	if maxB <= 0 {
		maxB = 30 * time.Second
	}
	backoff := base

	for {
		select {
		case <-st.stop:
			return
		default:
		}

		st.mu.Lock()
		dial := st.dial
		st.mu.Unlock()

		if dial == nil {
			st.mu.Lock()
			prov := st.lastProv
			st.mu.Unlock()
			d, err := m.dial(st.hostID, prov)
			if err != nil {
				st.mu.Lock()
				if st.dial == nil {
					st.lastErr = err.Error()
				}
				st.mu.Unlock()
				log.Printf("Host forward reconnect failed for %s (retry in %s): %s", st.hostID, backoff, err)
				if !m.sleepOrStop(st, backoff) {
					return
				}
				backoff = min(backoff*2, maxB)
				continue
			}
			st.mu.Lock()
			if st.dial == nil {
				// stop 可能在我们拨号期间被关闭(release):此时 state 已
				// 从管理器摘除,不能再把连接/转发装上去,装了就成了无人
				// 看管的孤儿。
				select {
				case <-st.stop:
					st.mu.Unlock()
					_ = d.Close()
					return
				default:
				}
				st.dial = d
				st.lastErr = ""
				log.Printf("Host forward connection established for %s", st.hostID)
				m.reconcileLocked(st)
				st.mu.Unlock()
				backoff = base
				continue
			}
			st.mu.Unlock()
			// 并发的 ensure 抢先建好了连接,丢掉这条多余的
			_ = d.Close()
			backoff = base
			continue
		}

		// 连接健在:等它死亡,或等 stop。
		done := m.connDoneFor(dial)
		select {
		case <-done:
			m.handleLost(st)
			if !m.sleepOrStop(st, backoff) {
				return
			}
			backoff = min(backoff*2, maxB)
		case <-st.stop:
			return
		}
	}
}

// connDoneFor 取连接死亡信号源(nil 注入时用默认实现)。
func (m *ForwardHostManager) connDoneFor(dr *sshx.DialResult) <-chan struct{} {
	if m.connDone != nil {
		return m.connDone(dr)
	}
	return defaultConnDone(dr)
}

// sleepOrStop 睡 d;stop 关闭时返回 false(supervise 应退出)。
func (m *ForwardHostManager) sleepOrStop(st *ForwardHostState, d time.Duration) bool {
	select {
	case <-st.stop:
		return false
	case <-time.After(d):
		return true
	}
}

// handleLost 连接断开:清掉挂在它上面的全部转发与连接本体,状态转
// failed("connection lost"),由 supervise 的下一轮重连拉起。
func (m *ForwardHostManager) handleLost(st *ForwardHostState) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.dial = nil
	st.lastErr = "connection lost"
	for key, entry := range st.entries {
		if entry.cancel != nil {
			entry.cancel()
		}
		delete(st.entries, key)
		st.failed[key] = "connection lost"
	}
	log.Printf("Host forward connection lost for %s", st.hostID)
}

// ensure 让主机的转发就绪(幂等):已有健康连接且转发在跑则直接返回;
// 否则带凭据拨号并启动 host.Forwards。拨号失败转交后台看管(Step B),
// 由 supervise 以指数退避重试,下次会话无需再等。由会话建立时调用
// (浏览器密码顺带成为转发连接的拨号凭据)。
func (m *ForwardHostManager) ensure(hostID string, prov *sshx.ProvidedSecrets) {
	if !m.hasEnabledForwards(hostID) {
		return
	}
	st := m.state(hostID)
	if prov != nil {
		st.mu.Lock()
		st.lastProv = prov
		st.mu.Unlock()
	}
	st.mu.Lock()
	if st.dial != nil {
		// 连接还在:补跑配置变更(主机更新也会走 reconcile,这里是兜底)
		m.reconcileLocked(st)
		st.mu.Unlock()
		return
	}

	dial, err := m.dial(hostID, prov)
	if err != nil {
		if st.dial == nil {
			st.lastErr = err.Error()
		}
		st.mu.Unlock()
		log.Printf("Host forward connection failed for %s: %s", hostID, err)
		m.resume(hostID, prov) // 转入后台重连,不拖住会话建立
		return
	}
	st.dial = dial
	st.lastErr = ""
	log.Printf("Host forward connection established for %s", hostID)
	m.reconcileLocked(st)
	st.mu.Unlock()
	m.resume(hostID, prov)
}

// reconcile 按主机当前配置 diff 启动/撤销转发(host.Forwards 变更后调用)。
// 无连接时交给后台看管去拨号;全部转发被停用时释放连接。
func (m *ForwardHostManager) reconcile(hostID string) {
	st := m.state(hostID)
	st.mu.Lock()
	if st.dial == nil {
		st.mu.Unlock()
		// 转发连接尚未建立:后台看管(幂等)负责拨号拉起
		if m.hasEnabledForwards(hostID) {
			m.resume(hostID, nil)
		}
		return
	}
	m.reconcileLocked(st)
	st.mu.Unlock()
	if !m.hasEnabledForwards(hostID) {
		// 全部转发已停用:连接不再需要,释放并停掉看管
		m.release(hostID)
	}
}

// reconcileLocked 在持有 st.mu 的情况下同步配置与运行态。
// 停用的转发视同不存在:在跑的撤销,不在跑的不启动。
func (m *ForwardHostManager) reconcileLocked(st *ForwardHostState) {
	want := m.specs(st.hostID)
	wanted := map[string]host.Forward{}
	for _, f := range want {
		if !f.IsEnabled() {
			continue
		}
		wanted[forwardKey(f)] = f
	}

	// 撤销:配置里已不存在(或已换绑端口/已停用)的转发
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
		if !f.IsEnabled() {
			continue
		}
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

// release 停止并释放主机的转发连接与所有转发(主机删除 / 服务关闭时调用),
// 并让后台看管 goroutine 退出。
func (m *ForwardHostManager) release(hostID string) {
	m.mu.Lock()
	st, ok := m.states[hostID]
	delete(m.states, hostID)
	m.mu.Unlock()
	if !ok {
		return
	}

	st.mu.Lock()
	supervising := st.supervising
	st.supervising = false
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
	close(st.stop)
	st.mu.Unlock()
	if supervising {
		log.Printf("Host forward supervision stopped for %s", hostID)
	}
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
		if !f.IsEnabled() {
			hf.Status = "disabled"
			out = append(out, hf)
			continue
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
