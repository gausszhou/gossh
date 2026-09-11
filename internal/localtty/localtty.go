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
		env:   shellEnv(o.Term),
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
// platform default; on Windows the default chain prefers PowerShell (what
// a modern Windows terminal opens) and falls back to %COMSPEC%.
func resolveShell() (string, []string) {
	if custom := strings.TrimSpace(os.Getenv("GOSSH_LOCAL_SHELL")); custom != "" {
		return custom, shellArgs(custom)
	}
	if runtime.GOOS == "windows" {
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

// shellArgs returns the arguments a shell needs to start interactive
// without printing a startup banner.
func shellArgs(shell string) []string {
	switch strings.ToLower(strings.TrimSuffix(filepath.Base(shell), ".exe")) {
	case "pwsh", "powershell":
		return []string{"-NoLogo"}
	default:
		return nil
	}
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
// On Windows TERM is left alone — ConPTY is not a TERM-based terminal.
func shellEnv(term string) []string {
	env := os.Environ()
	if runtime.GOOS == "windows" {
		return env
	}
	if term == "" {
		term = "xterm-256color"
	}
	return withEnv(env, "TERM", term)
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
