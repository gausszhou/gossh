package sshx

import (
	"fmt"
	"os"
	"runtime"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// AgentAuth returns an ssh.AuthMethod backed by the platform ssh-agent:
//   - Unix: $SSH_AUTH_SOCK
//   - Windows: the OpenSSH named pipe \\.\pipe\openssh-ssh-agent
//
// It returns (nil, nil) when no agent socket is present, so callers can
// fall through to key/password authentication.
func AgentAuth() (ssh.AuthMethod, error) {
	sockPath, err := agentSocketPath()
	if err != nil || sockPath == "" {
		return nil, err
	}
	conn, err := dialAgentSocket(sockPath)
	if err != nil {
		return nil, err
	}
	ag := agent.NewClient(conn)
	return ssh.PublicKeysCallback(ag.Signers), nil
}

// agentSocketPath returns the platform agent socket path.
func agentSocketPath() (string, error) {
	if runtime.GOOS == "windows" {
		return `\\.\pipe\openssh-ssh-agent`, nil
	}
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return "", fmt.Errorf("SSH_AUTH_SOCK is not set")
	}
	return sock, nil
}
