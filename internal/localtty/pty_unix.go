//go:build linux || darwin

package localtty

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// unixPTY is a local PTY backed by a /dev/ptmx master whose slave end is
// the shell's controlling terminal.
type unixPTY struct {
	master *os.File
	cmd    *exec.Cmd

	mu      sync.Mutex
	waitErr error
	exited  bool
	done    chan struct{}
}

// startPTY opens a PTY pair and starts the shell with the slave end as
// its controlling terminal (setsid + CTTY), so job control, signals and
// window-size changes behave exactly like they do over SSH.
func startPTY(cfg ptyConfig) (ptyHandle, error) {
	master, slavePath, err := openPTY()
	if err != nil {
		return nil, err
	}
	slave, err := os.OpenFile(slavePath, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		_ = master.Close()
		return nil, fmt.Errorf("failed to open pty slave `%s`: %w", slavePath, err)
	}

	// 初始尺寸设在从端上:shell 启动时读到的就是正确几何,首帧不会先
	// 按 80x24 画一次再被 resize 纠正。
	if cfg.cols > 0 && cfg.rows > 0 {
		_ = unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ,
			&unix.Winsize{Row: uint16(cfg.rows), Col: uint16(cfg.cols)})
	}

	cmd := exec.Command(cfg.shell, cfg.args...)
	cmd.Dir = cfg.dir
	cmd.Env = cfg.env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	// Setsid:新会话 + 新进程组(shell 成为组长 → 对 -pid 发信号即整组);
	// Setctty + Ctty=0:把 stdin(从端)设为控制终端,前台程序才能收到
	// SIGWINCH 与作业控制信号。
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}

	if err := cmd.Start(); err != nil {
		_ = slave.Close()
		_ = master.Close()
		return nil, wrapStartError(cfg.shell, err)
	}
	// 父进程只保留主端:两端都持有的话,shell 退出后主端读不到 EOF。
	_ = slave.Close()

	p := &unixPTY{master: master, cmd: cmd, done: make(chan struct{})}
	go p.waitLoop()
	return p, nil
}

// waitLoop records the exit status once the shell terminates.
func (p *unixPTY) waitLoop() {
	err := p.cmd.Wait()
	p.mu.Lock()
	p.waitErr = err
	p.exited = true
	p.mu.Unlock()
	close(p.done)
}

// Read returns shell output. Once the shell exits, Linux reports EIO on
// the master; it is translated to EOF so callers see a clean end of
// stream instead of a spurious read error.
func (p *unixPTY) Read(b []byte) (int, error) {
	n, err := p.master.Read(b)
	if err != nil && errors.Is(err, syscall.EIO) {
		return n, io.EOF
	}
	return n, err
}

// Write sends input to the shell.
func (p *unixPTY) Write(b []byte) (int, error) { return p.master.Write(b) }

// Resize applies TIOCSWINSZ on the master; the kernel then sends SIGWINCH
// to the shell's foreground process group.
func (p *unixPTY) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	return unix.IoctlSetWinsize(int(p.master.Fd()), unix.TIOCSWINSZ,
		&unix.Winsize{Row: uint16(rows), Col: uint16(cols)})
}

// Signal delivers sig to the shell's process group. The shell leads its
// own session/group (Setsid), so -pid addresses the foreground programs
// as well — the same semantics a terminal emulator has.
func (p *unixPTY) Signal(sig syscall.Signal) error {
	if p.Exited() {
		return ErrExited
	}
	if err := syscall.Kill(-p.cmd.Process.Pid, sig); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return ErrExited
		}
		return err
	}
	return nil
}

// Kill force-terminates the shell and its process group.
func (p *unixPTY) Kill() error {
	if p.Exited() {
		return nil
	}
	if err := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	return nil
}

// PID returns the shell process id.
func (p *unixPTY) PID() int {
	if p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// Wait blocks until the shell exits.
func (p *unixPTY) Wait() error {
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.waitErr
}

// Exited reports whether the shell has already exited.
func (p *unixPTY) Exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// Close releases the master end; the shell is expected to be gone by now
// (Close on the Tty signals/kills it first).
func (p *unixPTY) Close() error {
	return p.master.Close()
}
