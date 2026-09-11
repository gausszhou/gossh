package localtty

import (
	"bytes"
	"os"
	"path/filepath"
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

// TestShellArgs 各家族 shell 的启动参数。PowerShell 带 -NoLogo(否则欢迎
// 横幅占满首屏);bash 只在 Windows 上带 --login -i(见 shellArgs 注释)。
func TestShellArgs(t *testing.T) {
	bashArgs := []string(nil)
	if runtime.GOOS == "windows" {
		bashArgs = []string{"--login", "-i"}
	}
	cases := map[string][]string{
		"pwsh":                                            {"-NoLogo"},
		"pwsh.exe":                                        {"-NoLogo"},
		`C:\Program Files\PowerShell\7\pwsh.exe`:           {"-NoLogo"},
		`C:\Windows\System32\cmd.exe`:                      nil,
		`C:\Program Files\Git\bin\bash.exe`:                bashArgs,
		`C:\Program Files\Git\usr\bin\bash.exe`:            bashArgs,
		"/bin/bash":                                        bashArgs,
		"/bin/sh":                                          nil,
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

// TestGitBashNearFindsBashAboveGitExe git.exe 可能位于 <root>\cmd、
// <root>\bin 或 <root>\mingw64\bin,bash 所在目录总在其上层。
func TestGitBashNearFindsBashAboveGitExe(t *testing.T) {
	cases := []struct {
		name    string
		gitDir  []string
		bashDir []string
	}{
		{"git-in-cmd", []string{"cmd"}, []string{"bin"}},
		{"git-in-bin", []string{"bin"}, []string{"usr", "bin"}},
		{"git-in-mingw64-bin", []string{"mingw64", "bin"}, []string{"bin"}},
		// 没有同装的 bash:应当返回空串(向上走到盘符根为止,而任何支持的
		// 平台上都不存在 <盘根>\bin\bash.exe)。
		{"no-git-bash", []string{"cmd"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			gitPath := writeFakeExecutable(t, root, append(c.gitDir, "git.exe")...)
			want := ""
			if c.bashDir != nil {
				want = writeFakeExecutable(t, root, append(c.bashDir, "bash.exe")...)
			}
			if got := gitBashNear(gitPath); got != want {
				t.Fatalf("gitBashNear(%q) = %q, want %q", gitPath, got, want)
			}
		})
	}
}

// TestIsWSLBash System32 下的 bash.exe 是 WSL 启动器,不是 Git Bash。
func TestIsWSLBash(t *testing.T) {
	cases := map[string]bool{
		`C:\Windows\System32\bash.exe`:         true,
		`c:\windows\system32\bash.exe`:         true,
		`C:/Windows/System32/bash.exe`:         true,
		`C:\Program Files\Git\bin\bash.exe`:    false,
		`C:\Windows\System32\notbash\bash.exe`: false,
	}
	for path, want := range cases {
		if got := isWSLBash(path); got != want {
			t.Fatalf("isWSLBash(%q) = %v, want %v", path, got, want)
		}
	}
}

// TestEnvPathSkipsUnsetVariable 变量未设时必须返回空串 —— filepath.Join 对
// 空 base 会拼出一个相对路径,那是可被误当成真实路径的。
func TestEnvPathSkipsUnsetVariable(t *testing.T) {
	t.Setenv("GOSSH_TEST_EMPTY_BASE", "")
	if got := envPath("GOSSH_TEST_EMPTY_BASE", "Git", "bin"); got != "" {
		t.Fatalf("envPath with unset base = %q, want empty", got)
	}
	t.Setenv("GOSSH_TEST_BASE", "/opt/git")
	if got, want := envPath("GOSSH_TEST_BASE", "bin", "bash.exe"), filepath.Join("/opt/git", "bin", "bash.exe"); got != want {
		t.Fatalf("envPath = %q, want %q", got, want)
	}
}

// writeFakeExecutable 在 root/parts... 处建一个空文件并返回其路径。
func writeFakeExecutable(t *testing.T, root string, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{root}, parts...)...)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %q: %v", path, err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/true\n"), 0o755); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
	return path
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

// TestShellEnvTermFollowsShellKind TERM 只给读 TERM 的 shell:Windows 上
// cmd.exe/PowerShell 不得**注入** TERM(ConPTY 不是 TERM 型终端),而
// MSYS 风格的 bash(Git Bash)需要它,否则 vim/less 一类程序会告警;
// Unix 一律注入。
func TestShellEnvTermFollowsShellKind(t *testing.T) {
	t.Setenv("TERM", "dumb")
	cases := []struct {
		shell      string
		wantOnUnix bool
		wantOnWin  bool
	}{
		{`C:\Windows\System32\cmd.exe`, true, false},
		{`C:\Program Files\PowerShell\7\pwsh.exe`, true, false},
		{`C:\Program Files\Git\bin\bash.exe`, true, true},
		{"/bin/sh", true, true},
	}
	for _, c := range cases {
		env := shellEnv("xterm-256color", c.shell)
		injected := false
		for _, e := range env {
			if e == "TERM=xterm-256color" {
				injected = true
			}
		}
		want := c.wantOnUnix
		if runtime.GOOS == "windows" {
			want = c.wantOnWin
		}
		if injected != want {
			t.Fatalf("shellEnv(%q) injected TERM = %v, want %v (GOOS=%s)", c.shell, injected, want, runtime.GOOS)
		}
	}
}
