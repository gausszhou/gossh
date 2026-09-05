//go:build windows

package desktop

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// 开机自启:HKCU\<autostartRegPath> 下的 GoSSH 值,值为
// 引号包裹的可执行路径 + " app --no-browser"
// (自启静默进托盘、不弹浏览器,与 Linux autostart 条目语义一致)。
// autostartRegPath 声明为 var,便于测试覆盖为临时键而不触碰真实 Run 键。
var autostartRegPath = `Software\Microsoft\Windows\CurrentVersion\Run`

const autostartRegVal = "GoSSH"

// IsAutostart 自启状态 = Run 键下是否存在 GoSSH 值。
func IsAutostart() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, autostartRegPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(autostartRegVal)
	return err == nil
}

// SetAutostart 写入/删除 HKCU Run 键下的 GoSSH 值(用户级自启,无需管理员)。
func SetAutostart(enabled bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, autostartRegPath, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return fmt.Errorf("failed to open autostart registry key: %w", err)
	}
	defer k.Close()

	if !enabled {
		if err := k.DeleteValue(autostartRegVal); err != nil {
			if err == registry.ErrNotExist {
				return nil
			}
			return fmt.Errorf("failed to remove autostart value: %w", err)
		}
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to resolve the executable path: %w", err)
	}
	// 引号包裹路径(可能含空格),再拼自启参数
	cmdLine := `"` + strings.ReplaceAll(exe, `"`, `\"`) + `" app --no-browser`
	if err := k.SetStringValue(autostartRegVal, cmdLine); err != nil {
		return fmt.Errorf("failed to write autostart value: %w", err)
	}
	return nil
}
