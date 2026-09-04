package api

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/gausszhou/gossh/internal/utils"

	"golang.org/x/crypto/ssh"
)

// ForwardKind enumerates the supported port-forward types.
type ForwardKind string

const (
	ForwardLocal   ForwardKind = "local"   // -L: bind locally, forward to remote target
	ForwardRemote  ForwardKind = "remote"  // -R: bind on the remote side
	ForwardDynamic ForwardKind = "dynamic" // -D: local SOCKS5 proxy
)

// ForwardEntry is one active port forward attached to a session.
type ForwardEntry struct {
	ID     string      `json:"id"`
	Kind   ForwardKind `json:"kind"`
	Bind   string      `json:"bind"`
	Target string      `json:"target"` // empty for dynamic
	cancel func()
}

// ForwardRegistry tracks per-session active forwards.
type ForwardRegistry struct {
	mu        sync.Mutex
	bySession map[string]map[string]*ForwardEntry
	byEntryID map[string]string // entry id -> session id
}

// NewForwardRegistry creates an empty registry.
func NewForwardRegistry() *ForwardRegistry {
	return &ForwardRegistry{
		bySession: map[string]map[string]*ForwardEntry{},
		byEntryID: map[string]string{},
	}
}

func (f *ForwardRegistry) add(sessionID string, entry *ForwardEntry) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.bySession[sessionID] == nil {
		f.bySession[sessionID] = map[string]*ForwardEntry{}
	}
	f.bySession[sessionID][entry.ID] = entry
	f.byEntryID[entry.ID] = sessionID
}

func (f *ForwardRegistry) list(sessionID string) []*ForwardEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*ForwardEntry, 0, len(f.bySession[sessionID]))
	for _, e := range f.bySession[sessionID] {
		out = append(out, e)
	}
	return out
}

func (f *ForwardRegistry) get(sessionID, entryID string) (*ForwardEntry, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.bySession[sessionID][entryID]
	return e, ok
}

func (f *ForwardRegistry) remove(sessionID, entryID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.bySession[sessionID][entryID]; !ok {
		return false
	}
	delete(f.bySession[sessionID], entryID)
	delete(f.byEntryID, entryID)
	return true
}

// fillAddr normalizes a bind spec to "host:port" (defaults to 127.0.0.1).
func fillAddr(spec string) string {
	if strings.Contains(spec, ":") {
		return spec
	}
	return "127.0.0.1:" + spec
}

// startForward launches one forward on the session's ssh connection.
func (server *Server) startForward(sessionID string, kind ForwardKind, bind, target string) (*ForwardEntry, error) {
	sess, err := server.manager.Get(sessionID)
	if err != nil {
		return nil, fmt.Errorf("session not found")
	}
	client, err := sess.SSHClient()
	if err != nil {
		return nil, fmt.Errorf("session has no ssh connection")
	}
	bind = fillAddr(bind)

	entry := &ForwardEntry{
		ID:     utils.RandomString(8),
		Kind:   kind,
		Bind:   bind,
		Target: target,
	}

	switch kind {
	case ForwardLocal:
		ln, err := net.Listen("tcp", bind)
		if err != nil {
			return nil, fmt.Errorf("failed to listen on %s: %w", bind, err)
		}
		stop := make(chan struct{})
		var wg sync.WaitGroup
		entry.cancel = func() {
			close(stop)
			_ = ln.Close()
			wg.Wait()
		}
		go func() {
			for {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				wg.Add(1)
				go func() {
					defer wg.Done()
					remote, err := client.Dial("tcp", target)
					if err != nil {
						_ = conn.Close()
						return
					}
					pipe(conn, remote)
				}()
			}
		}()

	case ForwardRemote:
		ln, err := client.Listen("tcp", bind) // remote-side listen (-R)
		if err != nil {
			return nil, fmt.Errorf("failed to request remote listen on %s: %w", bind, err)
		}
		stop := make(chan struct{})
		var wg sync.WaitGroup
		entry.cancel = func() {
			close(stop)
			_ = ln.Close()
			wg.Wait()
		}
		go func() {
			for {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				wg.Add(1)
				go func() {
					defer wg.Done()
					local, err := net.Dial("tcp", target)
					if err != nil {
						_ = conn.Close()
						return
					}
					pipe(conn, local)
				}()
			}
		}()

	case ForwardDynamic:
		ln, err := net.Listen("tcp", bind)
		if err != nil {
			return nil, fmt.Errorf("failed to listen on %s: %w", bind, err)
		}
		var wg sync.WaitGroup
		entry.cancel = func() {
			_ = ln.Close()
			wg.Wait()
		}
		go func() {
			for {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				wg.Add(1)
				go func() {
					defer wg.Done()
					server.socks5(conn, client)
				}()
			}
		}()

	default:
		return nil, fmt.Errorf("unknown forward kind: %s", kind)
	}

	server.forwards.add(sessionID, entry)
	return entry, nil
}

// handleListForwards implements GET /api/sessions/{id}/forwards.
func (server *Server) handleListForwards(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, server.forwards.list(r.PathValue("id")))
}

// handleAddForward implements POST /api/sessions/{id}/forwards
// {"kind","bind","target"}.
func (server *Server) handleAddForward(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind   string `json:"kind"`
		Bind   string `json:"bind"`
		Target string `json:"target"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Bind == "" {
		writeError(w, http.StatusBadRequest, "kind and bind are required")
		return
	}
	kind := ForwardKind(req.Kind)
	switch kind {
	case ForwardLocal, ForwardRemote:
		if req.Target == "" {
			writeError(w, http.StatusBadRequest, "target is required for local/remote forwards")
			return
		}
	case ForwardDynamic:
	default:
		writeError(w, http.StatusBadRequest, "kind must be local, remote or dynamic")
		return
	}

	entry, err := server.startForward(r.PathValue("id"), kind, req.Bind, req.Target)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	log.Printf("Forward started on session %s: %s %s -> %s", r.PathValue("id"), kind, req.Bind, req.Target)
	writeJSON(w, http.StatusCreated, entry)
}

// handleDeleteForward implements DELETE /api/sessions/{id}/forwards/{fid}.
func (server *Server) handleDeleteForward(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	entryID := r.PathValue("fid")
	entry, ok := server.forwards.get(sessionID, entryID)
	if !ok {
		writeError(w, http.StatusNotFound, "forward not found")
		return
	}
	if entry.cancel != nil {
		entry.cancel()
	}
	server.forwards.remove(sessionID, entryID)
	w.WriteHeader(http.StatusNoContent)
}

// closeWriter mirrors net.Conn.CloseWrite where available.
type closeWriter interface{ CloseWrite() error }

func halfClose(c net.Conn) {
	if cw, ok := c.(closeWriter); ok {
		_ = cw.CloseWrite()
	}
}

// pipe bidirectionally copies two connections until either side ends.
func pipe(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() { io.Copy(a, b); halfClose(a); done <- struct{}{} }()
	go func() { io.Copy(b, a); halfClose(b); done <- struct{}{} }()
	<-done
	<-done
}

// socks5 runs a minimal SOCKS5 CONNECT server on the accepted connection,
// tunneling through the ssh client (dynamic forward, -D).
func (server *Server) socks5(conn net.Conn, client *ssh.Client) {
	defer conn.Close()

	// greeting: [0x05][nmethods][methods...]  (we accept no-auth)
	head := make([]byte, 2)
	if _, err := io.ReadFull(conn, head); err != nil || head[0] != 0x05 {
		return
	}
	rest := make([]byte, int(head[1]))
	if _, err := io.ReadFull(conn, rest); err != nil {
		return
	}
	if _, err := conn.Write([]byte{0x05, 0x00}); err != nil { // no-auth
		return
	}

	// request: [0x05][cmd][rsv][atyp][addr][port]
	reqHead := make([]byte, 4)
	if _, err := io.ReadFull(conn, reqHead); err != nil || reqHead[0] != 0x05 || reqHead[1] != 0x01 {
		return // only CONNECT
	}
	var host string
	switch reqHead[3] {
	case 0x01: // IPv4
		buf := make([]byte, 4)
		io.ReadFull(conn, buf)
		host = net.IP(buf).String()
	case 0x03: // domain
		l := make([]byte, 1)
		io.ReadFull(conn, l)
		buf := make([]byte, int(l[0]))
		io.ReadFull(conn, buf)
		host = string(buf)
	case 0x04: // IPv6
		buf := make([]byte, 16)
		io.ReadFull(conn, buf)
		host = net.IP(buf).String()
	default:
		return
	}
	portBuf := make([]byte, 2)
	io.ReadFull(conn, portBuf)
	port := binary.BigEndian.Uint16(portBuf)

	target := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	remote, err := client.Dial("tcp", target)
	if err != nil {
		return
	}
	defer remote.Close()

	// success reply: [0x05][0x00][0x00][0x01][0.0.0.0][0]
	if _, err := conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	pipe(conn, remote)
}
