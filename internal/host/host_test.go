package host

import (
	"strings"
	"testing"
)

func testHost(id, name string) *Host {
	return &Host{ID: id, Name: name, Address: "10.0.0." + strings.TrimPrefix(id, "h"), User: "root"}
}

func TestInventoryCRUD(t *testing.T) {
	inv, err := LoadInventory(t.TempDir() + "/hosts.json")
	if err != nil {
		t.Fatal(err)
	}

	a := testHost("h1", "web")
	if err := inv.Add(a); err != nil {
		t.Fatalf("add: %s", err)
	}
	// 重名拒绝
	if err := inv.Add(testHost("h2", "web")); err == nil {
		t.Fatal("duplicate name must be rejected")
	}
	// 重复 id 拒绝
	if err := inv.Add(testHost("h1", "web2")); err == nil {
		t.Fatal("duplicate id must be rejected")
	}
	// 缺字段拒绝
	if err := inv.Add(&Host{ID: "h3", Name: "x"}); err == nil {
		t.Fatal("incomplete host must be rejected")
	}

	if len(inv.List()) != 1 {
		t.Fatalf("expected 1 host, got %d", len(inv.List()))
	}

	// update
	got, _ := inv.Get("h1")
	got.User = "deploy"
	if err := inv.Update(got); err != nil {
		t.Fatalf("update: %s", err)
	}
	got2, _ := inv.Get("h1")
	if got2.User != "deploy" {
		t.Fatal("update not persisted")
	}

	// remove
	if err := inv.Remove("h1"); err != nil {
		t.Fatalf("remove: %s", err)
	}
	if _, err := inv.Get("h1"); err == nil {
		t.Fatal("removed host must not exist")
	}
}

func TestInventoryChainAndCycle(t *testing.T) {
	inv, err := LoadInventory(t.TempDir() + "/hosts.json")
	if err != nil {
		t.Fatal(err)
	}
	jump1 := testHost("j1", "jump1")
	jump2 := testHost("j2", "jump2")
	target := testHost("t1", "target")
	target.Via = "j2"
	jump2.Via = "j1"
	for _, h := range []*Host{jump1, jump2, target} {
		if err := inv.Add(h); err != nil {
			t.Fatal(err)
		}
	}

	chain, err := inv.Chain("t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) != 3 {
		t.Fatalf("expected 3 hops, got %d", len(chain))
	}
	// 最外层跳板在前,目标最后
	if chain[0].ID != "j1" || chain[1].ID != "j2" || chain[2].ID != "t1" {
		t.Fatalf("chain order wrong: %s %s %s", chain[0].ID, chain[1].ID, chain[2].ID)
	}

	parents, err := inv.Parents("t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(parents) != 2 || parents[0] != "j1" || parents[1] != "j2" {
		t.Fatalf("parents wrong: %v", parents)
	}

	// 删除被引用的跳板机应被拒绝
	if err := inv.Remove("j1"); err == nil {
		t.Fatal("removing a referenced jump host must be rejected")
	}
}

func TestInventoryCycleDetected(t *testing.T) {
	inv, err := LoadInventory(t.TempDir() + "/hosts.json")
	if err != nil {
		t.Fatal(err)
	}
	a := testHost("a", "a")
	b := testHost("b", "b")
	if err := inv.Add(a); err != nil {
		t.Fatal(err)
	}
	if err := inv.Add(b); err != nil {
		t.Fatal(err)
	}
	// a → b 合法
	a.Via = "b"
	if err := inv.Update(a); err != nil {
		t.Fatalf("a->b must be accepted: %s", err)
	}
	// b → a 成环,必须拒绝
	b.Via = "a"
	if err := inv.Update(b); err == nil {
		t.Fatal("via cycle must be rejected")
	}
}

func TestInventoryPersists(t *testing.T) {
	path := t.TempDir() + "/hosts.json"
	inv, err := LoadInventory(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = inv.Add(testHost("h1", "web"))

	reloaded, err := LoadInventory(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reloaded.Get("h1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "web" {
		t.Fatalf("persisted name wrong: %s", got.Name)
	}
}

func TestAddrDefaultsPort22(t *testing.T) {
	h := testHost("h1", "web")
	if h.Addr() != "10.0.0.1:22" {
		t.Fatalf("default port not applied: %s", h.Addr())
	}
	h.Port = 2222
	if h.Addr() != "10.0.0.1:2222" {
		t.Fatalf("custom port not honored: %s", h.Addr())
	}
}
