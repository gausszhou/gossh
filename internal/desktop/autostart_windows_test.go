//go:build windows

package desktop

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"
)

func TestSetAutostartToggle(t *testing.T) {
	// 指向临时测试键,绝不触碰真实的 HKCU\...\CurrentVersion\Run。
	const testKey = `Software\GoSSH-autostart-test`

	// 受限环境(沙箱/低完整性令牌等)可能拒绝注册表写入:优雅跳过,
	// 让正常用户会话与 CI 上仍完整执行。
	if k, _, err := registry.CreateKey(registry.CURRENT_USER, testKey, registry.QUERY_VALUE); err != nil {
		t.Skipf("registry write denied in this environment (restricted token?): %v", err)
	} else {
		k.Close()
	}

	orig := autostartRegPath
	autostartRegPath = testKey
	defer func() {
		autostartRegPath = orig
		_ = registry.DeleteKey(registry.CURRENT_USER, testKey)
	}()

	if IsAutostart() {
		t.Fatal("IsAutostart should be false at start")
	}

	if err := SetAutostart(true); err != nil {
		t.Fatalf("SetAutostart(true): %v", err)
	}
	if !IsAutostart() {
		t.Fatal("IsAutostart should be true after SetAutostart(true)")
	}

	k, err := registry.OpenKey(registry.CURRENT_USER, testKey, registry.QUERY_VALUE)
	if err != nil {
		t.Fatalf("open test key: %v", err)
	}
	val, _, err := k.GetStringValue(autostartRegVal)
	k.Close()
	if err != nil {
		t.Fatalf("read autostart value: %v", err)
	}
	for _, want := range []string{`"`, `app --no-browser`} {
		if !strings.Contains(val, want) {
			t.Fatalf("autostart value should contain %q, got %q", want, val)
		}
	}

	if err := SetAutostart(false); err != nil {
		t.Fatalf("SetAutostart(false): %v", err)
	}
	if IsAutostart() {
		t.Fatal("IsAutostart should be false after SetAutostart(false)")
	}
}
