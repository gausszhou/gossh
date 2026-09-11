//go:build darwin

package localtty

import (
	"bytes"
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// ptySlaveNameMax is the buffer size TIOCPTYGNAME expects (POSIX
// MAXPATHLEN on Darwin).
const ptySlaveNameMax = 128

// openPTY opens the master side of a fresh pty and returns the slave
// device path: /dev/ptmx + TIOCPTYGRANT/TIOCPTYUNLK(授权并解锁)+
// TIOCPTYGNAME(取从端路径)。
func openPTY() (*os.File, string, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, "", fmt.Errorf("failed to open /dev/ptmx: %w", err)
	}
	fd := int(master.Fd())

	if err := unix.IoctlSetPointerInt(fd, unix.TIOCPTYGRANT, 0); err != nil {
		_ = master.Close()
		return nil, "", fmt.Errorf("failed to grant pty: %w", err)
	}
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCPTYUNLK, 0); err != nil {
		_ = master.Close()
		return nil, "", fmt.Errorf("failed to unlock pty: %w", err)
	}

	// TIOCPTYGNAME 把从端路径写进调用方提供的缓冲区,没有 x/sys 包装,
	// 只能走原始 ioctl。
	var buf [ptySlaveNameMax]byte
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(unix.TIOCPTYGNAME),
		uintptr(unsafe.Pointer(&buf[0]))); errno != 0 {
		_ = master.Close()
		return nil, "", fmt.Errorf("failed to resolve pty slave name: %w", errno)
	}
	name := string(buf[:bytes.IndexByte(buf[:], 0)])
	if name == "" {
		_ = master.Close()
		return nil, "", fmt.Errorf("pty slave name is empty")
	}
	return master, name, nil
}
