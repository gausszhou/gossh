//go:build windows

package desktop

import (
	"fmt"
	"os/exec"
	"syscall"
)

// OpenBrowser 用 rundll32 + url.dll,FileProtocolHandler 在默认浏览器打开 url
// (不等待浏览器退出)。HideWindow 避免 rundll32 弹出控制台窗口。
func OpenBrowser(url string) error {
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to open browser (rundll32): %w", err)
	}
	go func() { _ = cmd.Wait() }() // 避免泄漏子进程句柄,不阻塞调用方
	return nil
}
