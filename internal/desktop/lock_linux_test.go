//go:build linux

package desktop

import (
	"path/filepath"
	"testing"
)

func TestTryLockSingleInstance(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "app.lock")

	release1, held1, err := TryLock(lockPath)
	if err != nil {
		t.Fatalf("first TryLock: %v", err)
	}
	if !held1 {
		t.Fatal("first TryLock should hold the lock")
	}

	// 第二实例:拿不到锁(held=false),不报错
	_, held2, err := TryLock(lockPath)
	if err != nil {
		t.Fatalf("second TryLock: %v", err)
	}
	if held2 {
		t.Fatal("second TryLock should NOT hold the lock")
	}

	// 释放后可重新获取
	release1()
	release3, held3, err := TryLock(lockPath)
	if err != nil {
		t.Fatalf("third TryLock after release: %v", err)
	}
	if !held3 {
		t.Fatal("third TryLock should hold the lock after release")
	}
	release3()
}

func TestTryLockCreatesParentDir(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "deep", "nested", "app.lock")
	release, held, err := TryLock(lockPath)
	if err != nil {
		t.Fatalf("TryLock with missing parent dir: %v", err)
	}
	if !held {
		t.Fatal("should hold the lock")
	}
	release()
}
