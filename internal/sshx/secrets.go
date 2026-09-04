package sshx

import (
	"errors"
	"fmt"
	"sync"

	"github.com/99designs/keyring"
)

// Secrets stores connection secrets (passwords and key passphrases).
//
// Persistence strategy (see CONTEXT.md → 凭据):
//   - OS keyring when available: Linux Secret Service, macOS Keychain,
//     Windows Credential Manager.
//   - Otherwise (headless Linux without a Secret Service daemon, etc.)
//     it degrades to a process-local memory store, so secrets are never
//     written to disk in plain text. No keyring daemon means secrets
//     are simply not persisted across restarts.
type Secrets struct {
	kr  keyring.Keyring
	mem sync.Map // fallback memory store
}

const (
	// serviceName identifies the keyring namespace.
	serviceName = "gossh"

	// passwordKeyPrefix / passphraseKeyPrefix are the keyring item names.
	passwordKeyPrefix   = "password:"
	passphraseKeyPrefix = "passphrase:"
)

// NewSecrets opens the OS keyring and falls back to memory when the
// platform keyring is unavailable.
func NewSecrets() *Secrets {
	kr, err := keyring.Open(keyring.Config{
		ServiceName:              serviceName,
		AllowedBackends:          keyring.AvailableBackends(),
		KeychainTrustApplication: true,
	})
	if err != nil {
		kr = nil // fall back to memory
	}
	return &Secrets{kr: kr}
}

// Available reports whether the OS keyring is in use (vs memory).
func (s *Secrets) Available() bool { return s.kr != nil }

func (s *Secrets) get(key string) (string, bool, error) {
	if s.kr != nil {
		item, err := s.kr.Get(key)
		if err == nil {
			return string(item.Data), true, nil
		}
		if !errors.Is(err, keyring.ErrKeyNotFound) {
			// keyring glitch — degrade to memory rather than fail the login
			if v, ok := s.mem.Load(key); ok {
				return v.(string), true, nil
			}
			return "", false, nil
		}
	}
	if v, ok := s.mem.Load(key); ok {
		return v.(string), true, nil
	}
	return "", false, nil
}

func (s *Secrets) set(key, value string) error {
	if s.kr != nil {
		if err := s.kr.Set(keyring.Item{Key: key, Data: []byte(value)}); err == nil {
			return nil
		}
		// fall through to memory on keyring failure
	}
	s.mem.Store(key, value)
	return nil
}

func (s *Secrets) delete(key string) error {
	s.mem.Delete(key)
	if s.kr != nil {
		// 尽力而为:keyring 运行时不可用(如无守护进程)时,以内存操作为准
		_ = s.kr.Remove(key)
	}
	return nil
}

// canonical identities -------------------------------------------------

// identity is the canonical keyring key for a host credential.
// addr is the canonical "host:port" of the connection target.
func passwordKey(addr, user string) string {
	return fmt.Sprintf("%s%s@%s", passwordKeyPrefix, user, addr)
}

// GetPassword returns the saved password for user@addr.
func (s *Secrets) GetPassword(addr, user string) (string, bool, error) {
	return s.get(passwordKey(addr, user))
}

// SetPassword saves the password for user@addr into the keyring.
func (s *Secrets) SetPassword(addr, user, password string) error {
	return s.set(passwordKey(addr, user), password)
}

// DeletePassword forgets the saved password for user@addr.
func (s *Secrets) DeletePassword(addr, user string) error {
	return s.delete(passwordKey(addr, user))
}

// GetPassphrase returns the saved passphrase for a private key file path.
func (s *Secrets) GetPassphrase(keyPath string) (string, bool, error) {
	return s.get(passphraseKeyPrefix + keyPath)
}

// SetPassphrase saves the passphrase for a private key file path.
func (s *Secrets) SetPassphrase(keyPath, passphrase string) error {
	return s.set(passphraseKeyPrefix+keyPath, passphrase)
}

// DeletePassphrase forgets the passphrase for a private key file path.
func (s *Secrets) DeletePassphrase(keyPath string) error {
	return s.delete(passphraseKeyPrefix + keyPath)
}
