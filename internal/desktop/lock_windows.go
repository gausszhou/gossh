//go:build windows

package desktop

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

// homeDir 返回用户主目录(平台文件里定义,便于测试覆盖)。
func homeDir() (string, error) {
	return os.UserHomeDir()
}

// DesktopLockPath 返回单实例锁文件路径(供 cmd 拼写日志/报错信息)。
// Windows 上实际互斥由命名互斥体承担,该路径仅作展示/占位。
func DesktopLockPath() string { return desktopLockPath() }

// sanitizeMutexUser 消毒用户名:互斥体名不能含反斜杠(路径分隔符语义),
// 反斜杠/斜杠统一替换为下划线;空名回退 "default"。
func sanitizeMutexUser(user string) string {
	user = strings.NewReplacer("\\", "_", "/", "_").Replace(user)
	if user == "" {
		return "default"
	}
	return user
}

// mutexName 派生单实例命名互斥体名:
//   - Local\ 会话命名空间:无需 SeCreateGlobalPrivilege,普通用户可创建,
//     与 Linux 端 flock(~/.gossh/app.lock,每用户)的语义差异仅是
//     不同会话不互斥——托盘界面本就是按会话呈现的;
//   - 追加用户名:同一会话内不同用户互不干扰(Windows 登录会话恒有
//     USERNAME 环境变量;缺失时回退 "default")。
func mutexName() string {
	return `Local\gossh-app-` + sanitizeMutexUser(os.Getenv("USERNAME"))
}

// TryLock 尝试获取桌面形态的单实例锁。
//
// Windows 无 flock:用命名互斥体判存在性——第一实例 CreateMutex 创建并持有;
// 第二实例 CreateMutexW 返回 ERROR_ALREADY_EXISTS,此时 x/sys 包装器同时
// 返回错误与有效句柄(需关闭),按「已有实例」处理,不视为失败。
// 进程退出/崩溃时内核自动关闭句柄并销毁互斥体,不会残留死锁。
//
// 返回 held=true 表示本进程持锁(调用方需 defer release());
// held=false 表示已有实例持有(第二实例语义)。
func TryLock(path string) (release func(), held bool, err error) {
	name, err := windows.UTF16PtrFromString(mutexName())
	if err != nil {
		return nil, false, fmt.Errorf("failed to encode mutex name: %w", err)
	}
	h, err := windows.CreateMutex(nil, false, name)
	if err == windows.ERROR_ALREADY_EXISTS {
		if h != 0 {
			_ = windows.CloseHandle(h)
		}
		return nil, false, nil // 已有实例持有
	}
	if err != nil {
		return nil, false, fmt.Errorf("failed to create single-instance mutex: %w", err)
	}
	return func() { _ = windows.CloseHandle(h) }, true, nil
}
