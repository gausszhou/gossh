package sshx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"golang.org/x/crypto/ssh"
)

// DialHop is a fully resolved connection step: everything needed to dial
// the target host directly. (Jump hosts were removed in v0.1.2, so the
// multi-hop ProxyJump machinery is gone; only single-hop direct dialing
// remains.)
type DialHop struct {
	Addr    string              // canonical "host:port"
	User    string              // remote username
	Auth    []ssh.AuthMethod    // tried in order
	HostKey ssh.HostKeyCallback // TOFU callback
	Timeout time.Duration       // dial + handshake timeout; 0 → default
}

// DialResult owns the established connection to the target host.
type DialResult struct {
	Target *ssh.Client
}

// Dial connects to the target host directly from the local machine.
func Dial(ctx context.Context, hop *DialHop) (*DialResult, error) {
	if hop == nil {
		return nil, errors.New("sshx: nil dial hop")
	}
	client, err := dialDirect(ctx, hop)
	if err != nil {
		return nil, fmt.Errorf("sshx: dial %s@%s: %w", hop.User, hop.Addr, err)
	}
	return &DialResult{Target: client}, nil
}

// dialDirect dials the target over TCP.
func dialDirect(ctx context.Context, hop *DialHop) (*ssh.Client, error) {
	timeout := hop.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	dctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	d := &net.Dialer{}
	conn, err := d.DialContext(dctx, "tcp", hop.Addr)
	if err != nil {
		return nil, err
	}
	cfg := &ssh.ClientConfig{
		User:            hop.User,
		Auth:            hop.Auth,
		HostKeyCallback: hop.HostKey,
		Timeout:         timeout,
		ClientVersion:   "SSH-2.0-gossh",
	}
	nc, chans, reqs, err := ssh.NewClientConn(conn, hop.Addr, cfg)
	if err != nil {
		return nil, err
	}
	return ssh.NewClient(nc, chans, reqs), nil
}

// Close tears down the target connection.
func (r *DialResult) Close() error {
	if r == nil || r.Target == nil {
		return nil
	}
	return r.Target.Close()
}
