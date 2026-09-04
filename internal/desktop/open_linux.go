//go:build linux

package desktop

import (
	"fmt"
	"os/exec"
)

// OpenBrowser 用 xdg-open 在默认浏览器打开 url(不等待浏览器退出)。
// xdg-open 缺失时给出明确错误。
func OpenBrowser(url string) error {
	cmd := exec.Command("xdg-open", url)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to open browser (xdg-open): %w", err)
	}
	go func() { _ = cmd.Wait() }() // 避免僵尸进程,不阻塞调用方
	return nil
}
