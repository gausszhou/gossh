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

// 删除被引用为跳板机的主机:跳板机功能 v0.1.2 移除,此处仅验证删除任意主机。
func TestInventoryRemoveAnyHost(t *testing.T) {
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
	if err := inv.Remove(a.ID); err != nil {
		t.Fatalf("removing a host must succeed: %s", err)
	}
	if _, err := inv.Get(a.ID); err == nil {
		t.Fatal("host must be gone after remove")
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
