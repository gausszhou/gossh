// Package host implements the host inventory: the persisted collection of
// host records (Host Inventory, CONTEXT.md) and the resolution of the
// connection chain (jump hosts) from a host record.
package host

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultPort is used when a host record does not set a port.
const DefaultPort = 22

// Errors returned by the inventory.
var (
	ErrNotFound = errors.New("host not found")
	ErrIDExists = errors.New("host id already exists")
	ErrNameDup  = errors.New("host name already in use")
	ErrArgument = errors.New("invalid host argument")
)

// CredentialKind enumerates how a host authenticates.
type CredentialKind string

const (
	// CredDefault tries agent, then default key files, then keyring password.
	CredDefault CredentialKind = "default"
	// CredKey uses the private key file at KeyPath.
	CredKey CredentialKind = "key"
	// CredAgent uses the local ssh-agent only.
	CredAgent CredentialKind = "agent"
	// CredPassword uses password authentication only.
	CredPassword CredentialKind = "password"
)

// Credential describes how connections to this host authenticate.
// Passwords are never stored here — they live in the system keyring
// (see sshx.Secrets) or are supplied per-connect.
type Credential struct {
	Kind    CredentialKind `json:"kind"`
	KeyPath string         `json:"key_path,omitempty"` // for CredKey
}

// Forward is a persistent port-forward definition on a host record.
// It is applied when the host record's session is created, unless the
// per-session panel overrides it.
type Forward struct {
	Kind   string `json:"kind"`             // "local" | "remote" | "dynamic"
	Bind   string `json:"bind"`             // e.g. "127.0.0.1:8080"
	Target string `json:"target,omitempty"` // e.g. "localhost:80"; empty for dynamic
}

// Host is one host inventory record (CONTEXT.md → 主机).
type Host struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Address    string     `json:"address"`
	Port       int        `json:"port,omitempty"`
	User       string     `json:"user"`
	Credential Credential `json:"credential"`
	Forwards   []Forward  `json:"forwards,omitempty"`
	CreatedAt  int64      `json:"created_at"`
	UpdatedAt  int64      `json:"updated_at"`
}

// Addr returns the canonical "host:port" of the record.
func (h *Host) Addr() string {
	port := h.Port
	if port == 0 {
		port = DefaultPort
	}
	return net.JoinHostPort(h.Address, fmt.Sprintf("%d", port))
}

// NewID returns a fresh host id.
func NewID() string {
	return fmt.Sprintf("h_%d_%s", time.Now().UnixNano(), randomSuffix(4))
}

const idChars = "abcdefghijklmnopqrstuvwxyz0123456789"

func randomSuffix(n int) string {
	b := make([]byte, n)
	now := time.Now().UnixNano()
	for i := range b {
		b[i] = idChars[now%int64(len(idChars))]
		now /= int64(len(idChars))
	}
	return string(b)
}

// Inventory is the persisted host inventory (CONTEXT.md → 主机清单).
// The JSON file is the single source of truth; writes are atomic
// (tmp file + rename).
type Inventory struct {
	mu     sync.Mutex
	path   string
	hosts  map[string]*Host
	byName map[string]string // name -> id
}

type inventoryPayload struct {
	Version int              `json:"version"`
	Hosts   map[string]*Host `json:"hosts"`
}

// LoadInventory loads (or creates) the inventory at path.
func LoadInventory(path string) (*Inventory, error) {
	inv := &Inventory{path: path, hosts: map[string]*Host{}, byName: map[string]string{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return inv, nil
		}
		return nil, fmt.Errorf("failed to read inventory at `%s`: %w", path, err)
	}
	var payload inventoryPayload
	if len(strings.TrimSpace(string(data))) == 0 {
		return inv, nil
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse inventory at `%s`: %w", path, err)
	}
	for id, h := range payload.Hosts {
		inv.hosts[id] = h
		inv.byName[h.Name] = id
	}
	return inv, nil
}

// List returns all hosts sorted by name.
func (inv *Inventory) List() []*Host {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	out := make([]*Host, 0, len(inv.hosts))
	for _, h := range inv.hosts {
		out = append(out, cloneHost(h))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get returns a host by id.
func (inv *Inventory) Get(id string) (*Host, error) {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	h, ok := inv.hosts[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return cloneHost(h), nil
}

// Add validates and inserts a host record.
func (inv *Inventory) Add(h *Host) error {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	return inv.addLocked(h)
}

func (inv *Inventory) addLocked(h *Host) error {
	if h.ID == "" {
		return fmt.Errorf("%w: id is required", ErrArgument)
	}
	if _, exists := inv.hosts[h.ID]; exists {
		return fmt.Errorf("%w: %s", ErrIDExists, h.ID)
	}
	if h.Name == "" || h.Address == "" || h.User == "" {
		return fmt.Errorf("%w: name, address and user are required", ErrArgument)
	}
	if _, dup := inv.byName[h.Name]; dup {
		return fmt.Errorf("%w: %s", ErrNameDup, h.Name)
	}
	now := time.Now().Unix()
	h.CreatedAt = now
	h.UpdatedAt = now
	inv.hosts[h.ID] = cloneHost(h)
	inv.byName[h.Name] = h.ID
	return inv.saveLocked()
}

// Update replaces a host record in place (id unchanged).
func (inv *Inventory) Update(h *Host) error {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	old, ok := inv.hosts[h.ID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, h.ID)
	}
	if h.Name != "" && h.Name != old.Name {
		if id, dup := inv.byName[h.Name]; dup && id != h.ID {
			return fmt.Errorf("%w: %s", ErrNameDup, h.Name)
		}
	}
	h.CreatedAt = old.CreatedAt
	h.UpdatedAt = time.Now().Unix()
	inv.hosts[h.ID] = cloneHost(h)
	inv.byName[h.Name] = h.ID
	if old.Name != h.Name {
		delete(inv.byName, old.Name)
	}
	return inv.saveLocked()
}

// Remove deletes a host.
func (inv *Inventory) Remove(id string) error {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	h, ok := inv.hosts[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	delete(inv.hosts, id)
	delete(inv.byName, h.Name)
	return inv.saveLocked()
}

// saveLocked persists the inventory atomically. Caller must hold inv.mu.
func (inv *Inventory) saveLocked() error {
	if inv.path == "" {
		return nil // memory-only inventory (tests)
	}
	if err := os.MkdirAll(filepath.Dir(inv.path), 0o700); err != nil {
		return fmt.Errorf("failed to create %s: %w", filepath.Dir(inv.path), err)
	}
	payload := inventoryPayload{Version: 1, Hosts: inv.hosts}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	tmp := inv.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("failed to write %s: %w", tmp, err)
	}
	return os.Rename(tmp, inv.path)
}

func cloneHost(h *Host) *Host {
	c := *h
	if h.Forwards != nil {
		c.Forwards = append([]Forward(nil), h.Forwards...)
	}
	return &c
}
