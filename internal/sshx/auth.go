package sshx

import (
	"fmt"
	"os"
	"time"

	"github.com/gausszhou/gossh/internal/host"
	"github.com/gausszhou/gossh/internal/utils"

	"golang.org/x/crypto/ssh"
)

// ProvidedSecrets carries secrets supplied by the caller for a single
// connection attempt. They are used exactly once and never persisted
// (the browser modal case; the "save to keyring" choice goes through
// Secrets instead).
type ProvidedSecrets struct {
	Password   *string
	Passphrase *string
}

// BuildHop resolves one host record into a DialHop: its own
// authentication methods (agent / key / keyring password / provided
// per-connect secrets) and its own TOFU host-key callback.
func BuildHop(h *host.Host, secrets *Secrets, prov *ProvidedSecrets, kh *KnownHosts, timeout time.Duration) (*DialHop, error) {
	auths, err := buildAuthMethods(h, secrets, prov)
	if err != nil {
		return nil, err
	}
	if len(auths) == 0 {
		return nil, fmt.Errorf("no authentication method available for host `%s` (%s)", h.Name, h.Addr())
	}
	return &DialHop{
		Addr:    h.Addr(),
		User:    h.User,
		Auth:    auths,
		HostKey: kh.Callback(h.Addr()),
		Timeout: timeout,
	}, nil
}

// buildAuthMethods assembles the ssh.AuthMethod list for one host.
// Multiple methods are tried by x/crypto/ssh in order.
func buildAuthMethods(h *host.Host, secrets *Secrets, prov *ProvidedSecrets) ([]ssh.AuthMethod, error) {
	switch h.Credential.Kind {
	case host.CredAgent:
		a, err := AgentAuth()
		if err != nil {
			return nil, err
		}
		if a == nil {
			return nil, fmt.Errorf("no ssh-agent socket available for host `%s`", h.Name)
		}
		return []ssh.AuthMethod{a}, nil

	case host.CredPassword:
		pw, err := resolvePassword(h, secrets, prov)
		if err != nil {
			return nil, err
		}
		return []ssh.AuthMethod{ssh.Password(pw)}, nil

	case host.CredKey:
		if h.Credential.KeyPath == "" {
			return nil, fmt.Errorf("host `%s` uses key credential without a key path", h.Name)
		}
		auth, err := keyAuth(h.Credential.KeyPath, secrets, prov)
		if err != nil {
			return nil, err
		}
		return []ssh.AuthMethod{auth}, nil

	default: // CredDefault: agent → first usable default key → keyring password
		auths := []ssh.AuthMethod{}
		if a, err := AgentAuth(); err == nil && a != nil {
			auths = append(auths, a)
		}
		for _, p := range []string{"~/.ssh/id_ed25519", "~/.ssh/id_ecdsa", "~/.ssh/id_rsa"} {
			if auth, err := keyAuth(p, secrets, prov); err == nil {
				auths = append(auths, auth)
				break
			}
		}
		if pw, err := resolvePassword(h, secrets, prov); err == nil && pw != "" {
			auths = append(auths, ssh.Password(pw))
		}
		return auths, nil
	}
}

// resolvePassword returns the password for user@addr: the provided
// per-connect one wins, else the keyring one.
func resolvePassword(h *host.Host, secrets *Secrets, prov *ProvidedSecrets) (string, error) {
	if prov != nil && prov.Password != nil && *prov.Password != "" {
		return *prov.Password, nil
	}
	pw, ok, _ := secrets.GetPassword(h.Addr(), h.User)
	if !ok || pw == "" {
		return "", fmt.Errorf("no password for %s@%s (enter it and save to keyring, or pass it per-connect)", h.User, h.Addr())
	}
	return pw, nil
}

// keyAuth builds an ssh.AuthMethod from a private key file, resolving an
// encrypted key's passphrase from the per-connect value or the keyring.
func keyAuth(keyPath string, secrets *Secrets, prov *ProvidedSecrets) (ssh.AuthMethod, error) {
	path := utils.Expand(keyPath)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read key file `%s`: %w", path, err)
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		// Likely an encrypted key — resolve the passphrase and retry.
		passphrase, perr := resolvePassphrase(path, secrets, prov)
		if perr != nil {
			return nil, fmt.Errorf("failed to parse key file `%s` (%v) and %v", path, err, perr)
		}
		signer, err = ssh.ParsePrivateKeyWithPassphrase(data, []byte(passphrase))
		if err != nil {
			return nil, fmt.Errorf("failed to unlock key file `%s`: %w", path, err)
		}
	}
	return ssh.PublicKeys(signer), nil
}

// resolvePassphrase returns the passphrase for an encrypted key file:
// provided per-connect value wins, else the keyring.
func resolvePassphrase(path string, secrets *Secrets, prov *ProvidedSecrets) (string, error) {
	if prov != nil && prov.Passphrase != nil && *prov.Passphrase != "" {
		return *prov.Passphrase, nil
	}
	pp, ok, _ := secrets.GetPassphrase(path)
	if !ok || pp == "" {
		return "", fmt.Errorf("key file `%s` is encrypted; provide its passphrase (and optionally save it to the keyring)", path)
	}
	return pp, nil
}
