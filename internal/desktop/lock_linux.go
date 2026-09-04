//go:build linux

package desktop

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// homeDir 返回用户主目录(平台文件里定义,便于测试覆盖)。
func homeDir() (string, error) {
	return os.UserHomeDir()
}

// DesktopLockPath 返回单实例锁文件路径(供 cmd 拼写日志/报错信息)。
func DesktopLockPath() string { return desktopLockPath() }

// TryLock 尝试获取桌面形态的单实例锁(flock 非阻塞)。
// 返回 held=true 表示本进程持锁(调用方需 defer release());
// held=false 表示已有实例持有(第二实例语义)。
// flock 在进程退出/崩溃时由内核自动释放,不会残留死锁。
func TryLock(path string) (release func(), held bool, err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, false, fmt.Errorf("failed to create lock directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, fmt.Errorf("failed to open lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if err == syscall.EWOULDBLOCK {
			return nil, false, nil // 已有实例持锁
		}
		return nil, false, fmt.Errorf("failed to lock: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, true, nil
}
