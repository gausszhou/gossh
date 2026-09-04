// Package desktop 提供「桌面形态」(gossh app)的桌面外壳:
// 单实例锁、打开浏览器、系统托盘、开机自启。
//
// 平台分层:
//   - 纯 Go 逻辑(锁/自启/开浏览器):linux 实现 + 非 linux 报错 stub
//   - 托盘:仅 `linux && cgo` 启用(getlantern/systray 走 GTK/AppIndicator);
//     linux 但非 cgo 构建时给出明确报错(保持 make build/release 的
//     CGO_ENABLED=0 五平台矩阵不受影响);非 linux 为 P2 占位报错。
package desktop

// TrayOptions 配置托盘常驻。
type TrayOptions struct {
	// UIURL 是浏览器可达的完整界面 URL(含 token),由服务端 URL hook 提供。
	UIURL string

	// Stop 在托盘「退出」被点击时调用,用于取消服务端运行上下文。
	Stop func()

	// Closed 在服务端退出后关闭;托盘收到后随之退出(服务挂了托盘不留)。
	Closed <-chan struct{}
}

// desktopLockPath 返回桌面形态的单实例锁文件路径。
// 与数据文件同居 ~/.gossh,常量路径保证第二实例能命中同一把锁。
func desktopLockPath() string {
	return desktopHome() + "/app.lock"
}

func desktopHome() string {
	home, err := homeDir()
	if err != nil || home == "" {
		return "/tmp/gossh-desktop"
	}
	return home + "/.gossh"
}
