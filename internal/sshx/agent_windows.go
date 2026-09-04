//go:build windows

package sshx

import (
	"net"

	"github.com/Microsoft/go-winio"
)

// dialAgentSocket connects to the OpenSSH agent named pipe on Windows.
func dialAgentSocket(path string) (net.Conn, error) {
	return winio.DialPipe(path, nil)
}
