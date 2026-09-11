package api

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gausszhou/gossh/internal/host"
	"github.com/gausszhou/gossh/internal/sshx"

	"golang.org/x/crypto/ssh"
)

var (
	errFakeDial   = errors.New("fake dial failed")
	errFakeLaunch = errors.New("fake launch failed")
)

// fakeDial 记录拨号调用(含凭据),可配置失败次数。
type fakeDial struct {
	mu          sync.Mutex
	calls       []string // "hostID"（无凭据标记）或 "hostID:pass:phrase"
	failFirst   int      // 前 N 次拨号失败
	closeCount  int
	closedHosts map[string]int
}

func (d *fakeDial) closeTracking(hostID string) {
	d.mu.Lock()
	if d.closedHosts == nil {
		d.closedHosts = map[string]int{}
	}
	d.closedHosts[hostID]++
	d.mu.Unlock()
}

func (d *fakeDial) fn(hostID string, prov *sshx.ProvidedSecrets) (*sshx.DialResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	mark := hostID
	if prov != nil && prov.Password != nil && *prov.Password != "" {
		mark += ":" + *prov.Password
	}
	if prov != nil && prov.Passphrase != nil && *prov.Passphrase != "" {
		mark += ":" + *prov.Passphrase
	}
	d.calls = append(d.calls, mark)
	if d.failFirst > 0 {
		d.failFirst--
		return nil, errFakeDial
	}
	return &sshx.DialResult{}, nil
}

// callList 返回拨号记录快照(supervise 在后台并发写,读取必须加锁)。
func (d *fakeDial) callList() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.calls...)
}

// fakeLaunch 记录转发启动调用,返回带 cancel 的条目;failKey 命中则失败。
type fakeLaunch struct {
	mu        sync.Mutex
	started   []string // "kind|bind|target"
	cancelled []string
	failKeys  map[string]bool
}

// startedKeys / cancelledKeys 返回记录快照(读取须加锁,理由同上)。
func (l *fakeLaunch) startedKeys() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.started...)
}

func (l *fakeLaunch) cancelledKeys() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.cancelled...)
}

func (l *fakeLaunch) fn(client *ssh.Client, kind ForwardKind, bind, target string) (*ForwardEntry, error) {
	key := string(kind) + "|" + bind + "|" + target
	l.mu.Lock()
	defer l.mu.Unlock()
	l.started = append(l.started, key)
	if l.failKeys[key] {
		return nil, errFakeLaunch
	}
	return &ForwardEntry{
		ID:     "e-" + key,
		Kind:   kind,
		Bind:   bind,
		Target: target,
		cancel: func() { l.recordCancel(key) },
	}, nil
}

func (l *fakeLaunch) recordCancel(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cancelled = append(l.cancelled, key)
}

type fakeSpecs struct {
	mu    sync.Mutex
	hosts map[string][]host.Forward
}

func (s *fakeSpecs) fn(hostID string) []host.Forward {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hosts[hostID]
}

// fakeHosts 可配置的主机清单(resumeAll 用)。
type fakeHosts struct {
	mu  sync.Mutex
	ids []string
}

func (h *fakeHosts) fn() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.ids...)
}

// fakeConn 伪造连接死亡信号:每次 connDone 记录 channel,测试手动关闭
// 最新的一条来模拟断线。
type fakeConn struct {
	mu   sync.Mutex
	died []chan struct{}
}

func (f *fakeConn) done(*sshx.DialResult) <-chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan struct{})
	f.died = append(f.died, ch)
	return ch
}

func (f *fakeConn) kill() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.died) > 0 {
		close(f.died[len(f.died)-1])
		f.died = f.died[:len(f.died)-1]
	}
}

// watching 报告 supervise 是否已注册连接死亡信号(测试里先等它注册再 kill,
// 否则 kill 落空)。
func (f *fakeConn) watching() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.died) > 0
}

// waitUntil 轮询直到 cond 成立(后台 supervise goroutine 是异步的)。
func waitUntil(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for: %s", msg)
}

func spec(kind, bind, target string) host.Forward {
	return host.Forward{Kind: kind, Bind: bind, Target: target}
}

func disabledSpec(kind, bind, target string) host.Forward {
	no := false
	return host.Forward{Kind: kind, Bind: bind, Target: target, Enabled: &no}
}

func buildManager(t *testing.T, specs *fakeSpecs, dial *fakeDial, launch *fakeLaunch) *ForwardHostManager {
	t.Helper()
	return NewForwardHostManager(dial.fn, launch.fn, specs.fn, func() []string { return nil })
}

func TestEnsureDialsOnceAndStartsForwards(t *testing.T) {
	specs := &fakeSpecs{hosts: map[string][]host.Forward{
		"h1": {spec("local", "127.0.0.1:18080", "localhost:80")},
	}}
	dial, launch := &fakeDial{}, &fakeLaunch{}
	m := buildManager(t, specs, dial, launch)

	m.ensure("h1", nil)
	m.ensure("h1", nil) // 幂等:不再拨号、不再重复启动
	m.ensure("h1", nil)

	if len(dial.callList()) != 1 {
		t.Fatalf("expected 1 dial, got %d", len(dial.callList()))
	}
	if len(launch.startedKeys()) != 1 {
		t.Fatalf("expected 1 forward started, got %d: %v", len(launch.startedKeys()), launch.startedKeys())
	}
	view := m.list("h1")
	if len(view) != 1 || view[0].Status != "running" {
		t.Fatalf("expected 1 running forward, got %+v", view)
	}
}

func TestEnsureUsesProvidedSecrets(t *testing.T) {
	specs := &fakeSpecs{hosts: map[string][]host.Forward{
		"h1": {spec("local", "127.0.0.1:18081", "localhost:80")},
	}}
	dial, launch := &fakeDial{}, &fakeLaunch{}
	m := buildManager(t, specs, dial, launch)

	pass, phrase := "s3cret", "kp"
	prov := &sshx.ProvidedSecrets{Password: &pass, Passphrase: &phrase}
	m.ensure("h1", prov)

	if len(dial.callList()) != 1 || dial.callList()[0] != "h1:s3cret:kp" {
		t.Fatalf("expected dial with credentials, got %v", dial.callList())
	}
}

func TestEnsureFailureExposesFailedAndRetries(t *testing.T) {
	specs := &fakeSpecs{hosts: map[string][]host.Forward{
		"h1": {spec("local", "127.0.0.1:18082", "localhost:80")},
	}}
	dial, launch := &fakeDial{failFirst: 1}, &fakeLaunch{}
	m := buildManager(t, specs, dial, launch)

	m.ensure("h1", nil) // 第一次拨号失败
	view := m.list("h1")
	if len(view) != 1 || view[0].Status != "failed" || view[0].Error == "" {
		t.Fatalf("expected failed view with error, got %+v", view)
	}
	if len(launch.startedKeys()) != 0 {
		t.Fatalf("no forward should start when dial fails, got %v", launch.startedKeys())
	}

	m.ensure("h1", nil) // 重试成功
	if len(dial.callList()) != 2 {
		t.Fatalf("expected retry dial, got %d", len(dial.callList()))
	}
	view = m.list("h1")
	if view[0].Status != "running" {
		t.Fatalf("expected running after retry, got %+v", view)
	}
}

func TestReconcileSyncsConfigChange(t *testing.T) {
	specs := &fakeSpecs{hosts: map[string][]host.Forward{
		"h1": {
			spec("local", "127.0.0.1:18083", "localhost:80"),
			spec("dynamic", "127.0.0.1:1080", ""),
		},
	}}
	dial, launch := &fakeDial{}, &fakeLaunch{}
	m := buildManager(t, specs, dial, launch)
	m.ensure("h1", nil)
	if len(launch.startedKeys()) != 2 {
		t.Fatalf("expected 2 forwards, got %v", launch.startedKeys())
	}

	// 配置变更:移除 dynamic 转发,新增 remote 转发
	specs.hosts["h1"] = []host.Forward{
		spec("local", "127.0.0.1:18083", "localhost:80"),
		spec("remote", "0.0.0.0:2222", "localhost:22"),
	}
	m.reconcile("h1")

	if len(launch.cancelledKeys()) != 1 || launch.cancelledKeys()[0] != "dynamic|127.0.0.1:1080|" {
		t.Fatalf("expected removed dynamic forward cancelled, got %v", launch.cancelledKeys())
	}
	if len(launch.startedKeys()) != 3 {
		t.Fatalf("expected one new forward started, got %v", launch.startedKeys())
	}
	view := m.list("h1")
	if len(view) != 2 {
		t.Fatalf("expected 2 forwards listed, got %+v", view)
	}
	for _, v := range view {
		if v.Status != "running" {
			t.Fatalf("expected all running, got %+v", view)
		}
	}
}

func TestReconcileWhileDisconnectedIsNoop(t *testing.T) {
	specs := &fakeSpecs{hosts: map[string][]host.Forward{
		"h1": {spec("local", "127.0.0.1:18084", "localhost:80")},
	}}
	dial, launch := &fakeDial{failFirst: 5}, &fakeLaunch{}
	m := buildManager(t, specs, dial, launch)
	m.ensure("h1", nil) // 拨号失败,连接不存在
	m.reconcile("h1")   // 未建立转发连接:no-op
	if len(launch.startedKeys()) != 0 {
		t.Fatalf("reconcile without connection must not start forwards, got %v", launch.startedKeys())
	}
}

func TestFailedForwardVisibleAndRetriedOnReconcile(t *testing.T) {
	specs := &fakeSpecs{hosts: map[string][]host.Forward{
		"h1": {
			spec("local", "127.0.0.1:18085", "localhost:80"),
			spec("dynamic", "127.0.0.1:1081", ""), // 启动会失败
		},
	}}
	dial, launch := &fakeDial{}, &fakeLaunch{failKeys: map[string]bool{"dynamic|127.0.0.1:1081|": true}}
	m := buildManager(t, specs, dial, launch)
	m.ensure("h1", nil)

	view := m.list("h1")
	if len(view) != 2 {
		t.Fatalf("expected 2 forwards, got %+v", view)
	}
	var dyn, loc string
	for _, v := range view {
		switch v.Kind {
		case ForwardDynamic:
			dyn = v.Status
		case ForwardLocal:
			loc = v.Status
		}
	}
	if dyn != "failed" || loc != "running" {
		t.Fatalf("expected dynamic=failed local=running, got dynamic=%s local=%s", dyn, loc)
	}

	// 故障消除后 reconcile 重试成功
	delete(launch.failKeys, "dynamic|127.0.0.1:1081|")
	m.reconcile("h1")
	view = m.list("h1")
	for _, v := range view {
		if v.Status != "running" {
			t.Fatalf("expected all running after retry, got %+v", view)
		}
	}
}

func TestReleaseStopsAndRedisals(t *testing.T) {
	specs := &fakeSpecs{hosts: map[string][]host.Forward{
		"h1": {spec("local", "127.0.0.1:18086", "localhost:80")},
	}}
	dial, launch := &fakeDial{}, &fakeLaunch{}
	m := buildManager(t, specs, dial, launch)
	m.ensure("h1", nil)

	m.release("h1")
	if len(launch.cancelledKeys()) != 1 {
		t.Fatalf("release must cancel all forwards, got %v", launch.cancelledKeys())
	}
	if len(m.list("h1")) != 1 || m.list("h1")[0].Status != "pending" {
		t.Fatalf("after release host forwards should be pending, got %+v", m.list("h1"))
	}

	m.ensure("h1", nil) // 再次 ensure 重新拨号
	if len(dial.callList()) != 2 {
		t.Fatalf("expected re-dial after release, got %d", len(dial.callList()))
	}
	if len(m.list("h1")) != 1 || m.list("h1")[0].Status != "running" {
		t.Fatalf("expected running after re-ensure, got %+v", m.list("h1"))
	}
}

func TestCloseAllReleasesEveryHost(t *testing.T) {
	specs := &fakeSpecs{hosts: map[string][]host.Forward{
		"h1": {spec("local", "127.0.0.1:18087", "localhost:80")},
		"h2": {spec("dynamic", "127.0.0.1:1082", "")},
	}}
	dial, launch := &fakeDial{}, &fakeLaunch{}
	m := buildManager(t, specs, dial, launch)
	m.ensure("h1", nil)
	m.ensure("h2", nil)

	m.closeAll()
	if len(launch.cancelledKeys()) != 2 {
		t.Fatalf("closeAll must cancel all forwards, got %v", launch.cancelledKeys())
	}
}

func TestDisabledForwardIsSkippedAndStatusShown(t *testing.T) {
	specs := &fakeSpecs{hosts: map[string][]host.Forward{
		"h1": {
			spec("local", "127.0.0.1:18088", "localhost:80"),
			disabledSpec("dynamic", "127.0.0.1:1083", ""),
		},
	}}
	dial, launch := &fakeDial{}, &fakeLaunch{}
	m := buildManager(t, specs, dial, launch)

	m.ensure("h1", nil)
	if len(launch.startedKeys()) != 1 {
		t.Fatalf("disabled forward must not be launched, got %v", launch.startedKeys())
	}
	view := m.list("h1")
	var dyn *HostForward
	for i := range view {
		if view[i].Kind == ForwardDynamic {
			dyn = &view[i]
		}
	}
	if dyn == nil || dyn.Status != "disabled" {
		t.Fatalf("expected disabled status for dynamic forward, got %+v", view)
	}
}

// 停用一条在跑的转发应即时撤销;全部停用会释放连接,重新启用后由后台
// 看管重新拨号拉起(异步)。
func TestDisableCancelsRunningForward(t *testing.T) {
	key := "local|127.0.0.1:18089|localhost:80"
	specs := &fakeSpecs{hosts: map[string][]host.Forward{
		"h1": {spec("local", "127.0.0.1:18089", "localhost:80")},
	}}
	dial, launch := &fakeDial{}, &fakeLaunch{}
	m := buildManager(t, specs, dial, launch)
	m.baseBackoff = time.Millisecond
	m.ensure("h1", nil)

	specs.hosts["h1"] = []host.Forward{disabledSpec("local", "127.0.0.1:18089", "localhost:80")}
	m.reconcile("h1")
	if len(launch.cancelledKeys()) != 1 || launch.cancelledKeys()[0] != key {
		t.Fatalf("disabling must cancel the running forward, got %v", launch.cancelledKeys())
	}

	specs.hosts["h1"] = []host.Forward{spec("local", "127.0.0.1:18089", "localhost:80")}
	m.reconcile("h1") // 连接已释放,这里只触发后台重拨
	waitUntil(t, func() bool { return len(launch.startedKeys()) == 2 }, "re-enable to relaunch after redial")
}

// 全部转发停用后连接被释放,状态显示 disabled 而非 failed。
func TestAllDisabledReleasesConnection(t *testing.T) {
	specs := &fakeSpecs{hosts: map[string][]host.Forward{
		"h1": {spec("local", "127.0.0.1:18090", "localhost:80")},
	}}
	dial, launch := &fakeDial{}, &fakeLaunch{}
	m := buildManager(t, specs, dial, launch)
	m.ensure("h1", nil)

	specs.hosts["h1"] = []host.Forward{disabledSpec("local", "127.0.0.1:18090", "localhost:80")}
	m.reconcile("h1")

	view := m.list("h1")
	if len(view) != 1 || view[0].Status != "disabled" {
		t.Fatalf("expected disabled view, got %+v", view)
	}
}

// 连接断开后:挂在它上面的转发被标记 failed,后台看管重连成功并重拉转发。
func TestSuperviseReconnectsAfterConnectionLost(t *testing.T) {
	specs := &fakeSpecs{hosts: map[string][]host.Forward{
		"h1": {spec("local", "127.0.0.1:18091", "localhost:80")},
	}}
	dial, launch := &fakeDial{}, &fakeLaunch{}
	conn := &fakeConn{}
	m := buildManager(t, specs, dial, launch)
	m.connDone = conn.done
	m.baseBackoff = time.Millisecond

	m.ensure("h1", nil)
	if len(dial.callList()) != 1 {
		t.Fatalf("expected 1 dial, got %d", len(dial.callList()))
	}
	waitUntil(t, conn.watching, "supervise to watch the connection")

	conn.kill() // 断线
	waitUntil(t, func() bool { return len(dial.callList()) >= 2 }, "supervise to redial")
	waitUntil(t, func() bool {
		view := m.list("h1")
		return len(view) == 1 && view[0].Status == "running"
	}, "forward to run again after reconnect")

	// 重连后转发被重新启动(第一条已被 handleLost 撤销)
	if len(launch.startedKeys()) != 2 {
		t.Fatalf("expected forward relaunched after reconnect, got %v", launch.startedKeys())
	}
	if len(launch.cancelledKeys()) != 1 {
		t.Fatalf("expected old forward cancelled on connection lost, got %v", launch.cancelledKeys())
	}
}

// 拨号失败也由后台看管以指数退避重试,无需等待下一次会话。
func TestSuperviseRetriesDialFailureWithBackoff(t *testing.T) {
	specs := &fakeSpecs{hosts: map[string][]host.Forward{
		"h1": {spec("local", "127.0.0.1:18092", "localhost:80")},
	}}
	dial, launch := &fakeDial{failFirst: 2}, &fakeLaunch{}
	m := buildManager(t, specs, dial, launch)
	m.baseBackoff = time.Millisecond

	m.resume("h1", nil)
	waitUntil(t, func() bool { return len(dial.callList()) >= 3 }, "supervise to retry dial")
	waitUntil(t, func() bool {
		view := m.list("h1")
		return len(view) == 1 && view[0].Status == "running"
	}, "forward to run after backoff retries")
}

// ensure 带来的凭据要留给断线重连复用(仅内存)。
func TestReconnectReusesProvidedSecrets(t *testing.T) {
	specs := &fakeSpecs{hosts: map[string][]host.Forward{
		"h1": {spec("local", "127.0.0.1:18093", "localhost:80")},
	}}
	dial, launch := &fakeDial{}, &fakeLaunch{}
	conn := &fakeConn{}
	m := buildManager(t, specs, dial, launch)
	m.connDone = conn.done
	m.baseBackoff = time.Millisecond

	pass := "s3cret"
	m.ensure("h1", &sshx.ProvidedSecrets{Password: &pass})
	waitUntil(t, conn.watching, "supervise to watch the connection")

	conn.kill()
	waitUntil(t, func() bool { return len(dial.callList()) >= 2 }, "supervise to redial")
	if dial.callList()[1] != "h1:s3cret" {
		t.Fatalf("reconnect must reuse provided secrets, got %v", dial.callList())
	}
}

// release 停掉看管:之后不再重连,直到再次 resume/ensure。
func TestReleaseStopsSupervision(t *testing.T) {
	specs := &fakeSpecs{hosts: map[string][]host.Forward{
		"h1": {spec("local", "127.0.0.1:18094", "localhost:80")},
	}}
	dial, launch := &fakeDial{}, &fakeLaunch{}
	m := buildManager(t, specs, dial, launch)
	m.baseBackoff = time.Millisecond

	m.resume("h1", nil)
	waitUntil(t, func() bool { return len(dial.callList()) >= 1 }, "initial dial")

	m.release("h1")
	time.Sleep(20 * time.Millisecond)
	calls := len(dial.callList())
	time.Sleep(30 * time.Millisecond)
	if len(dial.callList()) != calls {
		t.Fatalf("supervise must stop redialing after release, calls %d -> %d", calls, len(dial.callList()))
	}
}

// resumeAll 只看管配置了启用转发的主机,并逐台拨号。
func TestResumeAllSupervisesHostsWithForwards(t *testing.T) {
	specs := &fakeSpecs{hosts: map[string][]host.Forward{
		"h1": {spec("local", "127.0.0.1:18095", "localhost:80")},
		"h2": {disabledSpec("local", "127.0.0.1:18096", "localhost:80")},
	}}
	hosts := &fakeHosts{ids: []string{"h1", "h2"}}
	dial, launch := &fakeDial{}, &fakeLaunch{}
	m := NewForwardHostManager(dial.fn, launch.fn, specs.fn, hosts.fn)
	m.baseBackoff = time.Millisecond

	m.resumeAll()
	waitUntil(t, func() bool { return len(dial.callList()) >= 1 }, "resumeAll to dial h1")
	time.Sleep(20 * time.Millisecond)
	for _, c := range dial.callList() {
		if c != "h1" {
			t.Fatalf("only h1 (enabled forwards) should be dialed, got %v", dial.callList())
		}
	}
}
