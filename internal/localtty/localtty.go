// Package localtty adapts a local PTY shell to the session.Terminal
// interface: the shell runs on the machine that hosts the gossh server
// (CONTEXT.md → 本地服务器), with no SSH connection and no credentials
// involved. It is the local counterpart of sshtty — same byte-stream
// contract — so the session manager, WS attach, screen mirror and the
// agent-driving API are unchanged.
package localtty

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gausszhou/gossh/internal/terminal"
	"github.com/gausszhou/gossh/internal/utils"
)

// ErrExited is returned when operating on a local shell that has exited.
var ErrExited = errors.New("local session exited")

// defaultCols/defaultRows are the initial PTY size. The browser client
// sends its real size right after attaching (same contract as sshtty).
const (
	defaultCols = 80
	defaultRows = 24
)

// Tty is a session.Terminal backed by a local PTY.
type Tty struct {
	pty ptyHandle

	command string
	args    []string

	// closeSignal/closeTimeout record the manager-configured close
	// semantics used by Close.
	closeSignal  syscall.Signal
	closeTimeout time.Duration

	exited  chan struct{}
	waitErr error

	sizeMu     sync.Mutex
	cols, rows int

	closeOnce sync.Once
	closeErr  error
}

// ptyConfig is the platform-independent spawn request.
type ptyConfig struct {
	shell string
	args  []string
	dir   string
	env   []string
	cols  int
	rows  int
}

// ptyHandle is the platform-specific side of a local terminal: a PTY plus
// the shell process attached to it. Implemented by pty_linux.go,
// pty_darwin.go and pty_windows.go.
type ptyHandle interface {
	// Read/Write move bytes between the session and the shell's PTY.
	// The PTY merges stdout+stderr, so there is a single output stream.
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)

	// Resize applies a new window size to the PTY (TIOCSWINSZ on Unix,
	// ResizePseudoConsole on Windows).
	Resize(cols, rows int) error

	// Signal delivers sig to the shell('s foreground process group)
	// where the platform supports signals; on Windows the termination
	// signals map to TerminateProcess and the rest are ignored.
	Signal(sig syscall.Signal) error

	// Kill force-terminates the shell.
	Kill() error

	// PID is the shell process id (0 when unknown).
	PID() int

	// Wait blocks until the shell exits and returns its exit error
	// (nil for exit code 0).
	Wait() error

	// Exited reports whether the shell has already exited.
	Exited() bool

	// Close releases the PTY handles (the process may already be gone).
	Close() error
}

// startPTY (spawn cfg.shell on a fresh PTY of the requested size) is
// implemented once per platform: pty_linux.go (/dev/ptmx + TIOCGPTN),
// pty_darwin.go (/dev/ptmx + TIOCPTY*), pty_windows.go (ConPTY) and
// pty_unsupported.go (everything else, returns an error).

// New spawns the local shell in a fresh PTY. opts carry the configured
// TERM and close semantics from the session manager; TERM is not exported
// to the shell on Windows, where ConPTY has no TERM concept.
func New(opts ...terminal.Option) (*Tty, error) {
	o := terminal.Apply(opts...)
	shell, args := resolveShell()

	p, err := startPTY(ptyConfig{
		shell: shell,
		args:  args,
		dir:   startDir(),
		env:   shellEnv(o.Term, shell),
		cols:  defaultCols,
		rows:  defaultRows,
	})
	if err != nil {
		return nil, err
	}

	t := &Tty{
		pty:          p,
		command:      shell,
		args:         args,
		closeSignal:  syscall.Signal(o.CloseSignal),
		closeTimeout: time.Duration(o.CloseTimeout) * time.Second,
		exited:       make(chan struct{}),
		cols:         defaultCols,
		rows:         defaultRows,
	}
	go t.waitLoop()
	return t, nil
}

// waitLoop marks the tty exited once the shell terminates.
func (t *Tty) waitLoop() {
	t.waitErr = t.pty.Wait()
	close(t.exited)
}

// Read implements io.Reader (shell output).
func (t *Tty) Read(p []byte) (int, error) { return t.pty.Read(p) }

// Write implements io.Writer (input to the shell).
func (t *Tty) Write(p []byte) (int, error) {
	if t.exitedNow() {
		return 0, ErrExited
	}
	return t.pty.Write(p)
}

// Resize applies a new window size to the local PTY. The kernel delivers
// SIGWINCH to the shell's foreground process group, so full-screen
// programs redraw exactly like they do over SSH.
func (t *Tty) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	t.sizeMu.Lock()
	t.cols, t.rows = cols, rows
	t.sizeMu.Unlock()
	return t.pty.Resize(cols, rows)
}

// Size returns the current PTY size (mirror/UI queries).
func (t *Tty) Size() (int, int) {
	t.sizeMu.Lock()
	defer t.sizeMu.Unlock()
	return t.cols, t.rows
}

// Signal forwards a signal to the shell.
func (t *Tty) Signal(sig syscall.Signal) error {
	if t.exitedNow() {
		return ErrExited
	}
	return t.pty.Signal(sig)
}

// Close ends the session: close signal, a grace period, then a hard kill.
// Mirrors sshtty.Close so both terminal kinds behave identically for the
// session manager.
func (t *Tty) Close() error {
	t.closeOnce.Do(func() {
		// 1. 礼貌关闭信号(默认 SIGHUP:登录 shell 会因此退出)
		_ = t.pty.Signal(t.closeSignal)

		// 2. 有限宽限,然后硬杀
		select {
		case <-t.exited:
		case <-time.After(t.closeTimeout):
			_ = t.pty.Kill()
			select {
			case <-t.exited:
			case <-time.After(2 * time.Second):
			}
		}

		t.closeErr = t.pty.Close()
	})
	return t.closeErr
}

// Exited reports whether the shell has terminated.
func (t *Tty) Exited() bool { return t.exitedNow() }

func (t *Tty) exitedNow() bool {
	select {
	case <-t.exited:
		return true
	default:
		return false
	}
}

// Wait blocks until the shell terminates and returns its error.
func (t *Tty) Wait() error {
	<-t.exited
	return t.waitErr
}

// PID returns the shell process id.
func (t *Tty) PID() int { return t.pty.PID() }

// Command returns the shell that was started (window titles).
func (t *Tty) Command() string { return t.command }

// Args returns the shell arguments (window titles).
func (t *Tty) Args() []string { return t.args }

// WindowTitleVariables exposes local fields to the title template. A local
// session has no host record, so the machine itself is the "host".
func (t *Tty) WindowTitleVariables() map[string]interface{} {
	hostname, _ := os.Hostname()
	return map[string]interface{}{
		"host":     hostname,
		"addr":     "127.0.0.1",
		"user":     utils.CurrentUser(),
		"hostname": hostname,
	}
}

// resolveShell picks the shell to run. GOSSH_LOCAL_SHELL overrides the
// platform default; on Windows the default chain is Git Bash (when Git for
// Windows is installed) → PowerShell → %COMSPEC%, and on Unix it is $SHELL.
func resolveShell() (string, []string) {
	if custom := strings.TrimSpace(os.Getenv("GOSSH_LOCAL_SHELL")); custom != "" {
		return custom, shellArgs(custom)
	}
	if runtime.GOOS == "windows" {
		// Git Bash 优先:它自带 Git 的工具链与一套 POSIX 环境,
		// 是 Windows 上最接近 SSH 会话的本地 shell。
		if bash := findGitBash(); bash != "" {
			return bash, shellArgs(bash)
		}
		for _, candidate := range []string{"pwsh.exe", "powershell.exe"} {
			if path, err := exec.LookPath(candidate); err == nil {
				return path, shellArgs(path)
			}
		}
		if comspec := strings.TrimSpace(os.Getenv("COMSPEC")); comspec != "" {
			return comspec, nil
		}
		return `C:\Windows\System32\cmd.exe`, nil
	}
	if sh := strings.TrimSpace(os.Getenv("SHELL")); sh != "" {
		return sh, nil
	}
	return "/bin/sh", nil
}

// gitBashLocs are the paths, relative to a Git for Windows install root,
// where its bash lives. bin\bash.exe is the launcher Git's own tooling
// uses; usr\bin\bash.exe is the real MSYS bash (same program).
var gitBashLocs = [][]string{{"bin", "bash.exe"}, {"usr", "bin", "bash.exe"}}

// findGitBash locates Git for Windows' bash, or returns "" when Git for
// Windows is not installed.
//
// A bare exec.LookPath("bash.exe") would not do: on a machine with WSL
// enabled it hits C:\Windows\System32\bash.exe, which starts a Linux
// distribution — another system entirely, with another filesystem view.
// So the search goes through git.exe (its directory tells us the install
// root), then a few well-known locations, and only then PATH.
func findGitBash() string {
	if git, err := exec.LookPath("git"); err == nil {
		if bash := gitBashNear(git); bash != "" {
			return bash
		}
	}
	for _, p := range []string{
		envPath("ProgramFiles", "Git", "bin", "bash.exe"),
		envPath("ProgramFiles", "Git", "usr", "bin", "bash.exe"),
		envPath("ProgramFiles(x86)", "Git", "bin", "bash.exe"),
		envPath("LOCALAPPDATA", "Programs", "Git", "bin", "bash.exe"),
	} {
		if p != "" && isFile(p) {
			return p
		}
	}
	if bash, err := exec.LookPath("bash.exe"); err == nil && !isWSLBash(bash) {
		return bash
	}
	return ""
}

// gitBashNear walks up from git.exe looking for the bash shipped by the
// same installation. git.exe sits in <root>\cmd, <root>\bin or
// <root>\mingw64\bin, so the install root is one or two levels up.
func gitBashNear(gitPath string) string {
	dir := filepath.Dir(gitPath)
	for {
		for _, rel := range gitBashLocs {
			if p := filepath.Join(append([]string{dir}, rel...)...); isFile(p) {
				return p
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// isWSLBash reports whether path is the WSL launcher Windows ships in
// System32 rather than a Git/MSYS bash.
//
// The match is done by hand instead of via filepath.Dir so it stays
// GOOS-independent: on a non-Windows host (e.g. CI building the linux
// binary) filepath.Dir would not treat backslashes as separators and
// would hand back the whole path, breaking the unit test that runs on
// every platform. We only care whether the parent directory is System32,
// so we strip the final element honoring both separators and compare.
func isWSLBash(path string) bool {
	lower := strings.ToLower(path)
	if i := strings.LastIndexAny(lower, `\/`); i >= 0 {
		lower = lower[:i]
	}
	return strings.HasSuffix(lower, `system32`)
}

// envPath joins parts under the named environment variable, returning ""
// when the variable is unset (filepath.Join on an empty base would yield a
// relative path).
func envPath(envVar string, parts ...string) string {
	base := strings.TrimSpace(os.Getenv(envVar))
	if base == "" {
		return ""
	}
	return filepath.Join(append([]string{base}, parts...)...)
}

// isFile reports whether path exists and is a regular file.
func isFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// shellArgs returns the arguments a shell needs to start interactively
// without printing a startup banner.
func shellArgs(shell string) []string {
	switch strings.TrimSuffix(strings.ToLower(shellBase(shell)), ".exe") {
	case "pwsh", "powershell":
		return []string{"-NoLogo"}
	case "bash":
		// Windows 上的 bash 是 Git Bash(或 WSL/cygwin):它的完整
		// 环境(PATH、提示符、别名)由 login shell 的 /etc/profile
		// 建立 —— Git for Windows 的 git-bash.exe 也正是用
		// `--login -i` 启动的。Unix 下 $SHELL 以何种方式启动由终端
		// 自己决定,这里不干预。
		if runtime.GOOS == "windows" {
			return []string{"--login", "-i"}
		}
		return nil
	default:
		return nil
	}
}

// shellBase returns the executable name of shell without its directory.
// Both separators are handled instead of filepath.Base: the PowerShell
// detection must work for Windows-style paths on every platform (the
// test table covers `C:\...\pwsh.exe` while CI runs on Linux, where
// filepath.Base only splits on "/").
func shellBase(shell string) string {
	if i := strings.LastIndexAny(shell, `/\`); i >= 0 {
		return shell[i+1:]
	}
	return shell
}

// startDir is where the local shell starts: the user's home directory
// (what opening a terminal does), falling back to the server's working
// directory.
func startDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		if st, err := os.Stat(home); err == nil && st.IsDir() {
			return home
		}
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return ""
}

// shellEnv builds the shell environment: the server's own environment
// plus TERM (the same value that would be requested from a remote PTY).
// On Windows cmd.exe and PowerShell have no TERM concept — ConPTY is not a
// TERM-based terminal — so they must not be given one; a MSYS/Cygwin shell
// (Git Bash) is, and its full-screen programs (vim, less, top) warn
// without it, so there TERM is set as well.
func shellEnv(term, shell string) []string {
	env := os.Environ()
	if runtime.GOOS == "windows" && !isUnixShell(shell) {
		return env
	}
	if term == "" {
		term = "xterm-256color"
	}
	return withEnv(env, "TERM", term)
}

// isUnixShell reports whether shell is a MSYS/Cygwin/POSIX-style shell,
// i.e. one that reads TERM.
func isUnixShell(shell string) bool {
	switch strings.TrimSuffix(strings.ToLower(shellBase(shell)), ".exe") {
	case "bash", "sh", "dash", "ksh", "zsh", "fish":
		return true
	default:
		return false
	}
}

// withEnv sets key=value, replacing any existing entry (duplicate entries
// would make lookups platform-dependent: glibc returns the first match).
func withEnv(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if !strings.HasPrefix(e, prefix) {
			out = append(out, e)
		}
	}
	return append(out, prefix+value)
}

// wrapStartError gives spawn failures a message the UI can show as-is.
func wrapStartError(shell string, err error) error {
	return fmt.Errorf("failed to start local shell `%s`: %w", shell, err)
}
