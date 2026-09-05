package api

import (
	"errors"
	"sync"
	"testing"

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

// fakeLaunch 记录转发启动调用,返回带 cancel 的条目;failKey 命中则失败。
type fakeLaunch struct {
	mu        sync.Mutex
	started   []string // "kind|bind|target"
	cancelled []string
	failKeys  map[string]bool
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

func spec(kind, bind, target string) host.Forward {
	return host.Forward{Kind: kind, Bind: bind, Target: target}
}

func buildManager(t *testing.T, specs *fakeSpecs, dial *fakeDial, launch *fakeLaunch) *ForwardHostManager {
	t.Helper()
	return NewForwardHostManager(dial.fn, launch.fn, specs.fn)
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

	if len(dial.calls) != 1 {
		t.Fatalf("expected 1 dial, got %d", len(dial.calls))
	}
	if len(launch.started) != 1 {
		t.Fatalf("expected 1 forward started, got %d: %v", len(launch.started), launch.started)
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

	if len(dial.calls) != 1 || dial.calls[0] != "h1:s3cret:kp" {
		t.Fatalf("expected dial with credentials, got %v", dial.calls)
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
	if len(launch.started) != 0 {
		t.Fatalf("no forward should start when dial fails, got %v", launch.started)
	}

	m.ensure("h1", nil) // 重试成功
	if len(dial.calls) != 2 {
		t.Fatalf("expected retry dial, got %d", len(dial.calls))
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
	if len(launch.started) != 2 {
		t.Fatalf("expected 2 forwards, got %v", launch.started)
	}

	// 配置变更:移除 dynamic 转发,新增 remote 转发
	specs.hosts["h1"] = []host.Forward{
		spec("local", "127.0.0.1:18083", "localhost:80"),
		spec("remote", "0.0.0.0:2222", "localhost:22"),
	}
	m.reconcile("h1")

	if len(launch.cancelled) != 1 || launch.cancelled[0] != "dynamic|127.0.0.1:1080|" {
		t.Fatalf("expected removed dynamic forward cancelled, got %v", launch.cancelled)
	}
	if len(launch.started) != 3 {
		t.Fatalf("expected one new forward started, got %v", launch.started)
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
	if len(launch.started) != 0 {
		t.Fatalf("reconcile without connection must not start forwards, got %v", launch.started)
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
	if len(launch.cancelled) != 1 {
		t.Fatalf("release must cancel all forwards, got %v", launch.cancelled)
	}
	if len(m.list("h1")) != 1 || m.list("h1")[0].Status != "pending" {
		t.Fatalf("after release host forwards should be pending, got %+v", m.list("h1"))
	}

	m.ensure("h1", nil) // 再次 ensure 重新拨号
	if len(dial.calls) != 2 {
		t.Fatalf("expected re-dial after release, got %d", len(dial.calls))
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
	if len(launch.cancelled) != 2 {
		t.Fatalf("closeAll must cancel all forwards, got %v", launch.cancelled)
	}
}
