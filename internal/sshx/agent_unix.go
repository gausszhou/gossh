//go:build !windows

package sshx

import "net"

// dialAgentSocket connects to a Unix agent socket.
func dialAgentSocket(path string) (net.Conn, error) {
	return net.Dial("unix", path)
}
