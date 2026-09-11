package localtty

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gausszhou/gossh/internal/terminal"
)

// localttyTestMarker 是写进 shell 的一行命令的输出标记。
const localttyTestMarker = "GOSSH_LOCAL_TTY_OK"

// localttyReadUntil 读本地终端输出,直到出现 marker 或超时。
// 返回读到的全部内容(供断言失败时打印)。
func localttyReadUntil(t *testing.T, term *Tty, marker string, timeout time.Duration) string {
	t.Helper()

	type chunk struct {
		data []byte
		err  error
	}
	ch := make(chan chunk, 1)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := term.Read(buf)
			if n > 0 {
				out := make([]byte, n)
				copy(out, buf[:n])
				ch <- chunk{data: out}
			}
			if err != nil {
				ch <- chunk{err: err}
				return
			}
		}
	}()

	var all bytes.Buffer
	deadline := time.After(timeout)
	for {
		select {
		case c := <-ch:
			if len(c.data) > 0 {
				all.Write(c.data)
				if strings.Contains(all.String(), marker) {
					return all.String()
				}
			}
			if c.err != nil {
				t.Fatalf("terminal closed before %q appeared: %v (output %q)", marker, c.err, all.String())
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %q; output so far %q", marker, all.String())
		}
	}
}

// TestTtyRunsShellAndResizes 端到端验证本地终端:能起 shell、能收发字节、
// 能 resize、shell 退出后 Wait 返回。
func TestTtyRunsShellAndResizes(t *testing.T) {
	term, err := New(terminal.WithTerm("xterm-256color"))
	if err != nil {
		t.Fatalf("failed to start local shell: %v", err)
	}
	defer func() { _ = term.Close() }()

	if pid := term.PID(); pid <= 0 {
		t.Fatalf("expected a live shell pid, got %d", pid)
	}
	if cols, rows := term.Size(); cols != defaultCols || rows != defaultRows {
		t.Fatalf("initial size = %dx%d, want %dx%d", cols, rows, defaultCols, defaultRows)
	}

	// 写一行 echo:PTY 会回显输入,shell 再输出执行结果 —— 无论哪种
	// shell(cmd/PowerShell/sh)都支持 echo <text>。
	if _, err := term.Write([]byte("echo " + localttyTestMarker + "\r")); err != nil {
		t.Fatalf("write to local shell failed: %v", err)
	}
	localttyReadUntil(t, term, localttyTestMarker, 30*time.Second)

	// resize 必须生效且不报错(内核/ConPTY 会给前台程序发 SIGWINCH)
	if err := term.Resize(120, 40); err != nil {
		t.Fatalf("resize failed: %v", err)
	}
	if cols, rows := term.Size(); cols != 120 || rows != 40 {
		t.Fatalf("size after resize = %dx%d, want 120x40", cols, rows)
	}

	// 退出 shell:Close 走 关闭信号 → 宽限 → 硬杀 的路径
	if err := term.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}
	if !term.Exited() {
		t.Fatalf("terminal still reported alive after Close")
	}
	// 幂等:再关一次不应 panic/报错
	if err := term.Close(); err != nil {
		t.Fatalf("second close failed: %v", err)
	}
}

// TestTtyWriteAfterExitFails Tty.Write 在 shell 退出后应返回 ErrExited
// (会话层据此停止往已死的 PTY 写输入)。
func TestTtyWriteAfterExitFails(t *testing.T) {
	term, err := New()
	if err != nil {
		t.Fatalf("failed to start local shell: %v", err)
	}
	defer func() { _ = term.Close() }()

	// 让 shell 立刻退出:exit 在 cmd/PowerShell/sh 里都有效
	if _, err := term.Write([]byte("exit\r")); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	select {
	case <-term.exited:
	case <-time.After(30 * time.Second):
		t.Fatalf("shell did not exit after `exit`")
	}
	if _, err := term.Write([]byte("echo late\r")); err != ErrExited {
		t.Fatalf("write after exit = %v, want ErrExited", err)
	}
}

// TestResolveShellHonorsEnvOverride GOSSH_LOCAL_SHELL 覆盖平台默认 shell。
func TestResolveShellHonorsEnvOverride(t *testing.T) {
	t.Setenv("GOSSH_LOCAL_SHELL", "/custom/shell")
	shell, args := resolveShell()
	if shell != "/custom/shell" {
		t.Fatalf("shell = %q, want the GOSSH_LOCAL_SHELL override", shell)
	}
	if len(args) != 0 {
		t.Fatalf("args = %v, want none for a non-PowerShell shell", args)
	}
}

// TestShellArgsPowerShellNoLogo PowerShell 家族带 -NoLogo(否则欢迎横幅
// 会占满首屏),其他 shell 不带参数。
func TestShellArgsPowerShellNoLogo(t *testing.T) {
	cases := map[string][]string{
		"pwsh":                                   {"-NoLogo"},
		"pwsh.exe":                               {"-NoLogo"},
		`C:\Program Files\PowerShell\7\pwsh.exe`: {"-NoLogo"},
		`C:\Windows\System32\cmd.exe`:            nil,
		"/bin/bash":                              nil,
	}
	for shell, want := range cases {
		got := shellArgs(shell)
		if len(got) != len(want) {
			t.Fatalf("shellArgs(%q) = %v, want %v", shell, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("shellArgs(%q) = %v, want %v", shell, got, want)
			}
		}
	}
}

// TestWithEnvReplacesExisting 同名变量必须被替换而不是追加:重复条目下
// getenv 的取值依赖 libc(glibc 取第一个),会读到旧值。
func TestWithEnvReplacesExisting(t *testing.T) {
	env := withEnv([]string{"TERM=dumb", "PATH=/bin"}, "TERM", "xterm-256color")
	count := 0
	for _, e := range env {
		if strings.HasPrefix(e, "TERM=") {
			count++
			if e != "TERM=xterm-256color" {
				t.Fatalf("TERM entry = %q, want the new value", e)
			}
		}
	}
	if count != 1 {
		t.Fatalf("TERM appears %d times, want exactly once (%v)", count, env)
	}
}

// TestShellEnvWindowsHasNoTerm Windows/ConPTY 不是 TERM 型终端:不得**注入**
// TERM(环境里本来就有的 TERM 原样透传,不做增删)。
func TestShellEnvWindowsHasNoTerm(t *testing.T) {
	t.Setenv("TERM", "dumb")
	env := shellEnv("xterm-256color")
	injected := false
	for _, e := range env {
		if e == "TERM=xterm-256color" {
			injected = true
		}
	}
	if runtime.GOOS == "windows" && injected {
		t.Fatalf("TERM must not be injected on Windows")
	}
	if runtime.GOOS != "windows" && !injected {
		t.Fatalf("TERM must be set on %s", runtime.GOOS)
	}
}
