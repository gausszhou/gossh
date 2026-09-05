//go:build windows

package desktop

import (
	"testing"
)

func TestTryLockSingleInstance(t *testing.T) {
	// 与 Linux 端同语义:第一实例持锁,第二实例 held=false 不报错,
	// 释放后可重新获取。Windows 上锁由命名互斥体承担,path 仅作占位。
	release1, held1, err := TryLock("unused")
	if err != nil {
		t.Fatalf("first TryLock: %v", err)
	}
	if !held1 {
		t.Fatal("first TryLock should hold the lock")
	}

	_, held2, err := TryLock("unused")
	if err != nil {
		t.Fatalf("second TryLock: %v", err)
	}
	if held2 {
		t.Fatal("second TryLock should NOT hold the lock")
	}

	release1()
	release3, held3, err := TryLock("unused")
	if err != nil {
		t.Fatalf("third TryLock after release: %v", err)
	}
	if !held3 {
		t.Fatal("third TryLock should hold the lock after release")
	}
	release3()
}

func TestSanitizeMutexUser(t *testing.T) {
	cases := []struct{ in, want string }{
		{`domain\user`, `domain_user`},
		{`user/name`, `user_name`},
		{`plain`, `plain`},
		{``, `default`},
	}
	for _, c := range cases {
		if got := sanitizeMutexUser(c.in); got != c.want {
			t.Errorf("sanitizeMutexUser(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMutexNameUsesUserName(t *testing.T) {
	t.Setenv("USERNAME", `DOMAIN\alice`)
	if got := mutexName(); got != `Local\gossh-app-DOMAIN_alice` {
		t.Fatalf("mutexName should embed the sanitized user, got %q", got)
	}
}
