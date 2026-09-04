package sshx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"golang.org/x/crypto/ssh"
)

// DialHop is one resolved connection step: everything needed to dial one
// host in the chain (a jump host or the final target).
type DialHop struct {
	Addr    string              // canonical "host:port"
	User    string              // remote username
	Auth    []ssh.AuthMethod    // tried in order
	HostKey ssh.HostKeyCallback // TOFU callback for this hop
	Timeout time.Duration       // dial + handshake timeout; 0 → default
}

// DialResult owns every ssh.Client in the chain. The final client is the
// target; intermediate clients are jump hops that must stay open for the
// lifetime of the session.
type DialResult struct {
	Target  *ssh.Client
	clients []*ssh.Client
}

// DialChain dials hops[0] directly from the local machine and each
// subsequent hop through the previous one (direct-tcpip channel, the
// ProxyJump semantics of CONTEXT.md → 连接链).
func DialChain(ctx context.Context, hops []*DialHop) (*DialResult, error) {
	if len(hops) == 0 {
		return nil, errors.New("sshx: empty connection chain")
	}
	result := &DialResult{}
	for i, hop := range hops {
		var (
			client *ssh.Client
			err    error
		)
		if i == 0 {
			client, err = dialDirect(ctx, hop)
		} else {
			client, err = dialVia(ctx, result.clients[i-1], hop)
		}
		if err != nil {
			result.Close()
			return nil, fmt.Errorf("sshx: hop %d (%s@%s): %w", i, hop.User, hop.Addr, err)
		}
		result.clients = append(result.clients, client)
	}
	result.Target = result.clients[len(result.clients)-1]
	return result, nil
}

// dialDirect dials the first hop over TCP.
func dialDirect(ctx context.Context, hop *DialHop) (*ssh.Client, error) {
	timeout := hop.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", hop.Addr)
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

// dialVia dials the next hop through an established ssh client.
func dialVia(ctx context.Context, via *ssh.Client, hop *DialHop) (*ssh.Client, error) {
	timeout := hop.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = deadline
	}
	dctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := via.DialContext(dctx, "tcp", hop.Addr)
	if err != nil {
		return nil, err
	}
	cfg := &ssh.ClientConfig{
		User:            hop.User,
		Auth:            hop.Auth,
		HostKeyCallback: hop.HostKey,
		ClientVersion:   "SSH-2.0-gossh",
	}
	nc, chans, reqs, err := ssh.NewClientConn(conn, hop.Addr, cfg)
	if err != nil {
		return nil, err
	}
	return ssh.NewClient(nc, chans, reqs), nil
}

// Close tears down every connection in the chain (target last).
func (r *DialResult) Close() error {
	var firstErr error
	for i := len(r.clients) - 1; i >= 0; i-- {
		if err := r.clients[i].Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
