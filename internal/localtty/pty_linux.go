//go:build linux

package localtty

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// openPTY opens the master side of a fresh pty and returns the slave
// device path: /dev/ptmx + TIOCSPTLCK(解锁)+ TIOCGPTN(取从端编号)。
func openPTY() (*os.File, string, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, "", fmt.Errorf("failed to open /dev/ptmx: %w", err)
	}
	fd := int(master.Fd())

	// TIOCSPTLCK:0 = 解锁从端(未解锁时 open /dev/pts/N 会得到 EIO)
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		_ = master.Close()
		return nil, "", fmt.Errorf("failed to unlock pty: %w", err)
	}
	n, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		_ = master.Close()
		return nil, "", fmt.Errorf("failed to resolve pty slave number: %w", err)
	}
	return master, fmt.Sprintf("/dev/pts/%d", n), nil
}
