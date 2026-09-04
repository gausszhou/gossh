//go:build linux

package desktop

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const autostartFileName = "gossh.desktop"

func autostartPath() string {
	home, err := homeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "autostart", autostartFileName)
}

// execLine 解析自启 Exec 行指向的可执行路径:
// AppImage 运行时优先用 $APPIMAGE(指向真实文件,而非临时挂载点),
// 否则回退 os.Executable()(裸二进制安装)。
func execLine() (string, error) {
	if app := os.Getenv("APPIMAGE"); app != "" {
		return app, nil
	}
	return os.Executable()
}

// IsAutostart 自启状态 = autostart 文件的存在性。
func IsAutostart() bool {
	p := autostartPath()
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}

// SetAutostart 创建/删除 ~/.config/autostart/gossh.desktop。
// 桌面条目 Exec 为 "<exec> app --no-browser"(自启静默进托盘,不弹浏览器)。
func SetAutostart(enabled bool) error {
	p := autostartPath()
	if p == "" {
		return fmt.Errorf("cannot resolve the user home directory")
	}
	if !enabled {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove autostart entry: %w", err)
		}
		return nil
	}
	exec, err := execLine()
	if err != nil {
		return fmt.Errorf("failed to resolve the executable path: %w", err)
	}
	// app --no-browser:---,-- 参数必须引号包裹(路径可能含空格)
	content := "[Desktop Entry]\n" +
		"Type=Application\n" +
		"Name=GoSSH\n" +
		"Comment=SSH client with a browser UI\n" +
		"Exec=\"" + strings.ReplaceAll(exec, `"`, `\"`) + "\" app --no-browser\n" +
		"Terminal=false\n" +
		"X-GNOME-Autostart-enabled=true\n"
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("failed to create autostart directory: %w", err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		return fmt.Errorf("failed to write autostart entry: %w", err)
	}
	return nil
}
