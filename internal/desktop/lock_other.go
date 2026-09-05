//go:build !linux && !windows

package desktop

import (
	"errors"
	"os"
	"runtime"
)

var errUnsupported = errors.New("desktop mode is not supported on " + runtime.GOOS + " yet (GoSSH desktop targets Linux and Windows; other platforms are planned)")

func homeDir() (string, error) { return os.UserHomeDir() }

// DesktopLockPath 返回锁文件路径(占位,当前平台不会真正使用)。
func DesktopLockPath() string { return desktopLockPath() }

// TryLock 未支持平台暂不可用。
func TryLock(path string) (release func(), held bool, err error) {
	return nil, false, errUnsupported
}
