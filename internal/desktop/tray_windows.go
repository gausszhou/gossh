//go:build windows

package desktop

import (
	_ "embed"
	"log"

	"github.com/getlantern/systray"
)

// 托盘图标:Windows 的 Shell_NotifyIcon/LoadImage 需要 .ico
// (由 assets/icon.png 同一素材封装的单张 256x256 PNG 图像,Vista+ 支持
// PNG-in-ICO;与 icon.png 一并维护,见 generate-icon 说明)。
//
//go:embed icon.ico
var trayIcon []byte

// RunTray 托盘常驻,阻塞直到托盘退出或服务端退出(Closed 关闭)。
// Windows 下 getlantern/systray 走 win32 消息循环,纯 Go 实现无需 cgo,
// 与 Linux 的 cgo(GTK/AppIndicator)实现共用同一套菜单语义。
// 「退出」菜单:取消服务(Stop)并退出托盘。
func RunTray(o TrayOptions) error {
	systray.Run(func() {
		systray.SetIcon(trayIcon)
		systray.SetTooltip("GoSSH")
		systray.SetTitle("GoSSH")

		openItem := systray.AddMenuItem("打开界面", "在浏览器中打开 GoSSH")
		autostartItem := systray.AddMenuItemCheckbox("开机自启", "登录时自动启动 GoSSH", IsAutostart())
		systray.AddSeparator()
		quitItem := systray.AddMenuItem("退出", "退出 GoSSH(关闭服务与全部会话)")

		go func() {
			for {
				select {
				case <-openItem.ClickedCh:
					if err := OpenBrowser(o.UIURL); err != nil {
						log.Printf("desktop: failed to open the browser: %s", err)
					}
				case <-autostartItem.ClickedCh:
					next := !autostartItem.Checked()
					if err := SetAutostart(next); err != nil {
						log.Printf("desktop: failed to %s autostart: %s", map[bool]string{true: "enable", false: "disable"}[next], err)
						continue
					}
					if next {
						autostartItem.Check()
					} else {
						autostartItem.Uncheck()
					}
				case <-quitItem.ClickedCh:
					if o.Stop != nil {
						o.Stop()
					}
					systray.Quit()
				case <-o.Closed:
					systray.Quit()
					return
				}
			}
		}()
	}, func() {})
	return nil
}
