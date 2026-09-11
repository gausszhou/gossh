package host

import (
	"errors"
	"path/filepath"
	"testing"
)

// newTestInventory 返回一个空的内存清单(路径指向临时目录,避免落盘到真实清单)。
func newTestInventory(t *testing.T) *Inventory {
	t.Helper()
	inv, err := LoadInventory(filepath.Join(t.TempDir(), "hosts.json"))
	if err != nil {
		t.Fatalf("LoadInventory: %v", err)
	}
	return inv
}

// TestLocalGetResolvesBuiltinHost 内置本地服务器没有清单记录,但 Get 必须
// 能解析它——会话创建路径靠 host id 取 spec,和真实主机走同一条路。
func TestLocalGetResolvesBuiltinHost(t *testing.T) {
	inv := newTestInventory(t)

	h, err := inv.Get(LocalID)
	if err != nil {
		t.Fatalf("Get(%q) = %v, want the built-in record", LocalID, err)
	}
	if !h.Builtin {
		t.Fatalf("built-in record must carry Builtin=true: %+v", h)
	}
	if !IsLocal(h.ID) {
		t.Fatalf("IsLocal(%q) = false", h.ID)
	}
	if h.Address != "127.0.0.1" {
		t.Fatalf("address = %q, want 127.0.0.1", h.Address)
	}
	if h.User == "" || h.Name == "" {
		t.Fatalf("built-in record must carry a display user and name: %+v", h)
	}
}

// TestLocalIsNotInInventoryList 本地服务器是常驻条目而非清单记录:
// List 不返回它(API 层单独拼在列表首位)。
func TestLocalIsNotInInventoryList(t *testing.T) {
	inv := newTestInventory(t)
	if err := inv.Add(&Host{ID: NewID(), Name: "um480xt", Address: "192.168.4.199", User: "gauss"}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	for _, h := range inv.List() {
		if IsLocal(h.ID) {
			t.Fatalf("List() must not contain the built-in local server: %+v", h)
		}
	}
	if got := len(inv.List()); got != 1 {
		t.Fatalf("List() returned %d hosts, want 1 (only the inventory record)", got)
	}
}

// TestLocalCannotBeAddedUpdatedRemoved 本地服务器不可增删改:任何一条路径
// 漏掉判断都会让它被写进 hosts.json 或被删掉。
func TestLocalCannotBeAddedUpdatedRemoved(t *testing.T) {
	inv := newTestInventory(t)

	if err := inv.Add(&Host{ID: LocalID, Name: "hijack", Address: "1.2.3.4", User: "root"}); !errors.Is(err, ErrBuiltin) {
		t.Fatalf("Add(local) = %v, want ErrBuiltin", err)
	}
	if err := inv.Update(&Host{ID: LocalID, Name: "hijack", Address: "1.2.3.4", User: "root"}); !errors.Is(err, ErrBuiltin) {
		t.Fatalf("Update(local) = %v, want ErrBuiltin", err)
	}
	if err := inv.Remove(LocalID); !errors.Is(err, ErrBuiltin) {
		t.Fatalf("Remove(local) = %v, want ErrBuiltin", err)
	}

	// 失败后清单仍然干净:没有把 local 写进去,也没有破坏其他记录
	if len(inv.List()) != 0 {
		t.Fatalf("inventory changed after rejected local mutations: %+v", inv.List())
	}
}
