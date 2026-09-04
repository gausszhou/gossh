// Package sshx implements the SSH client core of gossh: host-key trust
// (TOFU), credential resolution, and connection-chain dialing.
package sshx

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// KnownHosts is the TOFU trust store: the host keys gossh has learned.
// It is persisted at ~/.gossh/known_hosts as JSON. It intentionally does
// NOT interoperate with OpenSSH's ~/.ssh/known_hosts (see
// docs/adr/0004-tofu-host-key-trust.md).
type KnownHosts struct {
	mu   sync.Mutex
	path string
	pins map[string]HostPin
}

// HostPin is one learned host key, keyed by canonical "address:port".
type HostPin struct {
	KeyType   string `json:"key_type"`
	KeyBlob   []byte `json:"key_blob"` // ssh.PublicKey.Marshal()
	FirstSeen int64  `json:"first_seen"`
}

// HostKeyMismatch is returned when the server presented a key different
// from the pinned one for that address (possible MITM; the pin must be
// deleted explicitly before reconnecting).
type HostKeyMismatch struct {
	Addr        string
	PinnedType  string
	PinnedFp    string
	PresentType string
	PresentFp   string
}

func (e *HostKeyMismatch) Error() string {
	return fmt.Sprintf(
		"host key mismatch for %s: pinned %s (SHA256:%s), server presented %s (SHA256:%s); delete the pin to trust the new key",
		e.Addr, e.PinnedType, e.PinnedFp, e.PresentType, e.PresentFp)
}

// LoadKnownHosts loads the TOFU store from path, creating an empty store
// when the file does not exist yet.
func LoadKnownHosts(path string) (*KnownHosts, error) {
	k := &KnownHosts{path: path, pins: map[string]HostPin{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return k, nil
		}
		return nil, fmt.Errorf("failed to read known_hosts at `%s`: %w", path, err)
	}
	// tolerate empty file
	if len(bytes.TrimSpace(data)) == 0 {
		return k, nil
	}
	if err := json.Unmarshal(data, &k.pins); err != nil {
		return nil, fmt.Errorf("failed to parse known_hosts at `%s`: %w", path, err)
	}
	return k, nil
}

// Get returns the pinned key for a canonical address, if present.
func (k *KnownHosts) Get(addr string) (HostPin, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	pin, ok := k.pins[addr]
	return pin, ok
}

// Pinned returns true when the address has a pinned key.
func (k *KnownHosts) Pinned(addr string) bool {
	_, ok := k.Get(addr)
	return ok
}

// Pins returns a copy of all pinned addresses (for the management UI).
func (k *KnownHosts) Pins() map[string]HostPin {
	k.mu.Lock()
	defer k.mu.Unlock()
	out := make(map[string]HostPin, len(k.pins))
	for addr, pin := range k.pins {
		out[addr] = pin
	}
	return out
}

// Forget removes the pin for an address (opt-in trust reset).
func (k *KnownHosts) Forget(addr string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.pins, addr)
	return k.saveLocked()
}

// Pin records a host key for an address and persists the store.
func (k *KnownHosts) Pin(addr string, key ssh.PublicKey) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.pins[addr] = HostPin{
		KeyType:   key.Type(),
		KeyBlob:   key.Marshal(),
		FirstSeen: time.Now().Unix(),
	}
	return k.saveLocked()
}

// Callback returns an ssh.HostKeyCallback implementing TOFU:
//   - pinned key matches  -> accept
//   - pinned key differs  -> reject with HostKeyMismatch
//   - no pin yet          -> accept and pin (trust on first use)
func (k *KnownHosts) Callback(addr string) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		pin, ok := k.Get(addr)
		if !ok {
			if err := k.Pin(addr, key); err != nil {
				return fmt.Errorf("failed to pin host key for %s: %w", addr, err)
			}
			return nil
		}
		if pin.KeyType == key.Type() && bytes.Equal(pin.KeyBlob, key.Marshal()) {
			return nil
		}
		return &HostKeyMismatch{
			Addr:        addr,
			PinnedType:  pin.KeyType,
			PinnedFp:    Fingerprint(pin.KeyType, pin.KeyBlob),
			PresentType: key.Type(),
			PresentFp:   Fingerprint(key.Type(), key.Marshal()),
		}
	}
}

// Fingerprint renders a key in OpenSSH style: SHA256:<base64>.
func Fingerprint(keyType string, blob []byte) string {
	sum := sha256.Sum256(blob)
	return fmt.Sprintf("%s SHA256:%s", keyType, base64.StdEncoding.EncodeToString(sum[:]))
}

// saveLocked persists the store (tmp file + rename, so a crash never
// corrupts the trust data). Caller must hold k.mu.
func (k *KnownHosts) saveLocked() error {
	if k.path == "" {
		return nil // memory-only store (tests)
	}
	if err := os.MkdirAll(filepath.Dir(k.path), 0o700); err != nil {
		return fmt.Errorf("failed to create %s: %w", filepath.Dir(k.path), err)
	}
	data, err := json.MarshalIndent(k.pins, "", "  ")
	if err != nil {
		return err
	}
	tmp := k.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("failed to write %s: %w", tmp, err)
	}
	return os.Rename(tmp, k.path)
}
