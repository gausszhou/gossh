//go:build linux && cgo

package desktop

import (
	_ "embed"
	"log"

	"github.com/getlantern/systray"
)

// 托盘图标:与 assets/icon.png 同一份素材(go:embed 只能引用包内文件)。
//
//go:embed icon.png
var trayIcon []byte

// RunTray 托盘常驻,阻塞直到托盘退出或服务端退出(Closed 关闭)。
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
