//go:build !windows

package sshtty

import (
	"syscall"

	"golang.org/x/crypto/ssh"
)

// sigusrExtra resolves the Unix-only SIGUSR1/2 signals.
func sigusrExtra(sig syscall.Signal) (ssh.Signal, bool) {
	switch sig {
	case syscall.SIGUSR1:
		return ssh.SIGUSR1, true
	case syscall.SIGUSR2:
		return ssh.SIGUSR2, true
	default:
		return ssh.SIGHUP, false
	}
}
