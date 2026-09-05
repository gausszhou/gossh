// Package sshtty adapts an SSH connection to the
// session.Terminal interface: a remote PTY shell behaves like the local
// process gotty used to spawn. All bytes flow through the same wire
// protocol, so the rest of the stack (session manager, WS attach, mirror)
// is unchanged.
package sshtty

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"syscall"
	"time"

	"github.com/gausszhou/gossh/internal/host"
	"github.com/gausszhou/gossh/internal/sshx"
	"github.com/gausszhou/gossh/internal/terminal"

	"golang.org/x/crypto/ssh"
)

// Tty is a session.Terminal backed by an SSH connection.
type Tty struct {
	conn *sshx.DialResult
	host *host.Host // target host record (display and titles)

	sess *ssh.Session

	// closeSignal/closeTimeout record the manager-configured close
	// semantics used by Close.
	closeSignal  syscall.Signal
	closeTimeout time.Duration

	// stdin is the write side to the remote PTY; output streams from
	// the remote PTY arrive on out (stdout+stderr merged by the PTY).
	stdin  io.WriteCloser
	out    io.Reader
	closed bool

	sizeMu sync.Mutex
	cols   int
	rows   int

	waitOnce sync.Once
	exited   chan struct{}
	waitErr  error

	closeOnce sync.Once
	closeErr  error

	stopKA chan struct{}
}

var (
	// ErrExited is returned when operating on a session whose remote
	// process has already exited.
	ErrExited = errors.New("remote session exited")
)

// New opens a shell on the target of conn (the chain must already be
// dialed). opts carry the configured TERM and close semantics from the
// session manager.
func New(conn *sshx.DialResult, h *host.Host, opts ...terminal.Option) (*Tty, error) {
	o := terminal.Apply(opts...)
	term := o.Term
	if term == "" {
		term = "xterm-256color"
	}
	cols, rows := 80, 24

	sess, err := conn.Target.NewSession()
	if err != nil {
		return nil, fmt.Errorf("failed to open ssh session: %w", err)
	}

	modes := ssh.TerminalModes{
		ssh.ECHO:          1, // echo typed input
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := sess.RequestPty(term, rows, cols, modes); err != nil {
		_ = sess.Close()
		return nil, fmt.Errorf("failed to request pty: %w", err)
	}

	stdin, err := sess.StdinPipe()
	if err != nil {
		_ = sess.Close()
		return nil, fmt.Errorf("failed to open stdin pipe: %w", err)
	}
	// With a PTY the remote merges stdout+stderr into the channel;
	// only Stdout is wired (Stderr left unset so no duplicate stream).
	out, err := sess.StdoutPipe()
	if err != nil {
		_ = sess.Close()
		return nil, fmt.Errorf("failed to open stdout pipe: %w", err)
	}

	if err := sess.Shell(); err != nil {
		_ = sess.Close()
		return nil, fmt.Errorf("failed to start remote shell: %w", err)
	}

	t := &Tty{
		conn:         conn,
		host:         h,
		sess:         sess,
		closeSignal:  syscall.Signal(o.CloseSignal),
		closeTimeout: time.Duration(o.CloseTimeout) * time.Second,
		stdin:        stdin,
		out:          out,
		cols:         cols,
		rows:         rows,
		exited:       make(chan struct{}),
		stopKA:       make(chan struct{}),
	}

	go t.waitLoop()
	go t.keepalive()

	return t, nil
}

// waitLoop marks the tty exited once the remote shell terminates.
func (t *Tty) waitLoop() {
	t.waitErr = t.sess.Wait()
	close(t.exited)
}

// keepalive keeps otherwise-idle connections from
// being dropped by NATs and servers. Best-effort: failures surface on
// the session channel itself, where the output pump reports them.
func (t *Tty) keepalive() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-t.stopKA:
			return
		case <-t.exited:
			return
		case <-ticker.C:
			_, _ = t.sess.SendRequest("keepalive@openssh.com", true, nil)
		}
	}
}

// Read implements io.Reader (remote terminal output).
func (t *Tty) Read(p []byte) (int, error) { return t.out.Read(p) }

// Write implements io.Writer (input to the remote terminal).
func (t *Tty) Write(p []byte) (int, error) {
	if t.exitedNow() {
		return 0, ErrExited
	}
	return t.stdin.Write(p)
}

// Resize sends a window-change request to the remote PTY.
func (t *Tty) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	t.sizeMu.Lock()
	t.cols, t.rows = cols, rows
	t.sizeMu.Unlock()
	return t.sess.WindowChange(rows, cols)
}

// Size returns the current PTY size (mirror/UI queries).
func (t *Tty) Size() (int, int) {
	t.sizeMu.Lock()
	defer t.sizeMu.Unlock()
	return t.cols, t.rows
}

// Signal forwards a signal to the remote process via the ssh "signal"
// channel request.
func (t *Tty) Signal(sig syscall.Signal) error {
	return t.sess.Signal(signalToSSH(sig))
}

// Close ends the session: close signal, a grace period, then a hard
// SIGKILL, then all chain connections.
func (t *Tty) Close() error {
	t.closeOnce.Do(func() {
		close(t.stopKA)

		// 1. polite close signal (default SIGHUP)
		_ = t.sess.Signal(signalToSSH(t.closeSignal))

		// 2. bounded grace, then SIGKILL
		select {
		case <-t.exited:
		case <-time.After(t.closeTimeout):
			_ = t.sess.Signal(ssh.SIGKILL)
			select {
			case <-t.exited:
			case <-time.After(2 * time.Second):
			}
		}

		_ = t.sess.Close()
		t.closeErr = t.conn.Close()
	})
	return t.closeErr
}

// Exited reports whether the remote shell has terminated.
func (t *Tty) Exited() bool { return t.exitedNow() }

func (t *Tty) exitedNow() bool {
	select {
	case <-t.exited:
		return true
	default:
		return false
	}
}

// Wait blocks until the remote shell terminates and returns its error.
func (t *Tty) Wait() error {
	<-t.exited
	return t.waitErr
}

// PID of a remote process is not observable over SSH; report 0.
func (t *Tty) PID() int { return 0 }

// Command returns the host display name (window titles).
func (t *Tty) Command() string { return t.host.Name }

// Args returns the target address (window titles).
func (t *Tty) Args() []string { return []string{t.host.Addr()} }

// WindowTitleVariables exposes host fields to the title template.
func (t *Tty) WindowTitleVariables() map[string]interface{} {
	return map[string]interface{}{
		"host":  t.host.Name,
		"addr":  t.host.Addr(),
		"user":  t.host.User,
		"group": t.host.Group,
	}
}

// SSHClient exposes the underlying target client so the same connection
// can host SFTP sessions and port forwards (session-scoped).
func (t *Tty) SSHClient() *ssh.Client { return t.conn.Target }

// signalToSSH maps a syscall.Signal to the ssh signal name.
func signalToSSH(sig syscall.Signal) ssh.Signal {
	switch sig {
	case syscall.SIGHUP:
		return ssh.SIGHUP
	case syscall.SIGINT:
		return ssh.SIGINT
	case syscall.SIGQUIT:
		return ssh.SIGQUIT
	case syscall.SIGTERM:
		return ssh.SIGTERM
	case syscall.SIGKILL:
		return ssh.SIGKILL
	default:
		if sig, ok := sigusrExtra(sig); ok {
			return sig
		}
		return ssh.SIGHUP
	}
}

// sigusrExtra maps the non-cross-platform SIGUSR1/2 (defined only on
// Unix) to their ssh signals. Implemented per-platform: see
// sigusr_unix.go / sigusr_windows.go.
