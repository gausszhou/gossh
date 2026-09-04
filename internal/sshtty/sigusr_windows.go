//go:build windows

package sshtty

import (
	"syscall"

	"golang.org/x/crypto/ssh"
)

// sigusrExtra - SIGUSR1/2 do not exist on Windows; never reported a match,
// so callers map them onto SIGHUP (the safe fallback).
func sigusrExtra(sig syscall.Signal) (ssh.Signal, bool) {
	return ssh.SIGHUP, false
}
