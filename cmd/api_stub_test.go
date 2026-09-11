package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"testing"

	"github.com/gausszhou/gossh/internal/host"
	"github.com/gausszhou/gossh/internal/session"
)

// ---------------------------------------------------------------------------
// a stateful stand-in for a running server
// ---------------------------------------------------------------------------

// apiStub is a fake gossh server that actually keeps state, so a test can
// assert that a read-modify-write command (hosts edit, a host forward) wrote
// back what it should have rather than merely posting the right shape.
type apiStub struct {
	*httptest.Server

	mu       sync.Mutex
	recMu    sync.Mutex  // guards posted/created only, so a handler may record while holding mu
	hosts    []host.Host // real records, no builtin
	forwards map[string][]map[string]any
	pins     []knownHostPin
	title    string
	posted   map[string]map[string]any
}

func (s *apiStub) record(name string, body map[string]any) {
	s.recMu.Lock()
	defer s.recMu.Unlock()
	if s.posted == nil {
		s.posted = map[string]map[string]any{}
	}
	s.posted[name] = body
}

func (s *apiStub) last(name string) map[string]any {
	s.recMu.Lock()
	defer s.recMu.Unlock()
	return s.posted[name]
}

func (s *apiStub) hostByID(id string) (host.Host, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, h := range s.hosts {
		if h.ID == id {
			return h, true
		}
	}
	return host.Host{}, false
}

func newAPIStub(t *testing.T, hosts []host.Host) *apiStub {
	t.Helper()
	s := &apiStub{
		hosts:    hosts,
		forwards: map[string][]map[string]any{},
		pins: []knownHostPin{
			{Addr: "10.0.0.5:22", KeyType: "ssh-ed25519", Fingerprint: "SHA256:aaa", FirstSeen: 1700000000},
		},
		title: "GoSSH",
	}

	authorized := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized: missing or invalid access token"}`))
			return false
		}
		return true
	}
	writeJSON := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	decode := func(r *http.Request) map[string]any {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body == nil {
			body = map[string]any{}
		}
		return body
	}
	decodeHost := func(r *http.Request) hostPayload {
		var p hostPayload
		_ = json.NewDecoder(r.Body).Decode(&p)
		return p
	}
	fail := func(w http.ResponseWriter, status int, message string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
	}

	mux := http.NewServeMux()

	// --- host inventory ---
	mux.HandleFunc("GET /api/hosts", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		builtin := host.Host{ID: "local", Name: "Local", Address: "127.0.0.1", User: "me", Builtin: true}
		writeJSON(w, append([]host.Host{builtin}, s.hosts...))
	})
	mux.HandleFunc("POST /api/hosts", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		p := decodeHost(r)
		p.Host.ID = "h_new"
		if p.SavePassword && p.Password != nil {
			s.record("host-password", map[string]any{"password": *p.Password, "save": p.SavePassword})
		}
		s.mu.Lock()
		s.hosts = append(s.hosts, p.Host)
		s.mu.Unlock()
		writeJSON(w, p.Host)
	})
	mux.HandleFunc("GET /api/hosts/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		h, ok := s.hostByID(r.PathValue("id"))
		if !ok {
			fail(w, http.StatusNotFound, "host not found")
			return
		}
		writeJSON(w, h)
	})
	mux.HandleFunc("PUT /api/hosts/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		if r.PathValue("id") == "local" {
			fail(w, http.StatusBadRequest, "built-in local server cannot be modified: local")
			return
		}
		p := decodeHost(r)
		s.mu.Lock()
		defer s.mu.Unlock()
		for i, h := range s.hosts {
			if h.ID == r.PathValue("id") {
				p.Host.ID = h.ID
				writeJSON(w, p.Host)
				s.hosts[i] = p.Host
				return
			}
		}
		fail(w, http.StatusNotFound, "host not found")
	})
	mux.HandleFunc("DELETE /api/hosts/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		if r.PathValue("id") == "local" {
			fail(w, http.StatusBadRequest, "built-in local server cannot be modified: local")
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		for i, h := range s.hosts {
			if h.ID == r.PathValue("id") {
				s.hosts = append(s.hosts[:i], s.hosts[i+1:]...)
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		fail(w, http.StatusNotFound, "host not found")
	})
	mux.HandleFunc("GET /api/hosts/{id}/forwards", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		h, ok := s.hostByID(r.PathValue("id"))
		if !ok {
			fail(w, http.StatusNotFound, "host not found")
			return
		}
		out := make([]map[string]any, 0, len(h.Forwards))
		for i, f := range h.Forwards {
			out = append(out, map[string]any{
				"id":     "hf_" + strconv.Itoa(i),
				"kind":   f.Kind,
				"bind":   f.Bind,
				"target": f.Target,
				"status": "running",
			})
		}
		writeJSON(w, out)
	})

	// --- sessions ---
	mux.HandleFunc("GET /api/sessions", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		writeJSON(w, map[string]any{"sessions": testSessions()})
	})
	mux.HandleFunc("PUT /api/sessions/{id}/title", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		body := decode(r)
		s.record("session-title", body)
		writeJSON(w, map[string]any{"title": body["title"]})
	})
	mux.HandleFunc("POST /api/sessions/{id}/resize", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		body := decode(r)
		s.record("session-resize", body)
		writeJSON(w, session.StateDescription{ID: r.PathValue("id"), State: "running"})
	})
	mux.HandleFunc("POST /api/sessions/{id}/signal", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		body := decode(r)
		s.record("session-signal", body)
		writeJSON(w, session.StateDescription{ID: r.PathValue("id"), State: "running"})
	})
	mux.HandleFunc("GET /api/sessions/{id}/forwards", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		out := s.forwards[r.PathValue("id")]
		if out == nil {
			out = []map[string]any{}
		}
		writeJSON(w, out)
	})
	mux.HandleFunc("POST /api/sessions/{id}/forwards", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		body := decode(r)
		s.record("session-forward-add", body)
		entry := map[string]any{
			"id": "f_new", "kind": body["kind"], "bind": body["bind"], "target": body["target"],
		}
		s.mu.Lock()
		s.forwards[r.PathValue("id")] = append(s.forwards[r.PathValue("id")], entry)
		s.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, entry)
	})
	mux.HandleFunc("DELETE /api/sessions/{id}/forwards/{fid}", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		s.record("session-forward-rm", map[string]any{"fid": r.PathValue("fid")})
		s.mu.Lock()
		delete(s.forwards, r.PathValue("id"))
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})

	// --- trust store ---
	mux.HandleFunc("GET /api/known-hosts", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		writeJSON(w, s.pins)
	})
	mux.HandleFunc("DELETE /api/known-hosts/{addr}", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		addr := r.PathValue("addr")
		s.record("forget", map[string]any{"addr": addr})
		s.mu.Lock()
		defer s.mu.Unlock()
		kept := s.pins[:0]
		for _, p := range s.pins {
			if p.Addr != addr {
				kept = append(kept, p)
			}
		}
		s.pins = kept
		w.WriteHeader(http.StatusNoContent)
	})

	// --- secrets ---
	mux.HandleFunc("POST /api/secrets", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		s.record("secret-set", decode(r))
		writeJSON(w, map[string]bool{"saved": true})
	})
	mux.HandleFunc("DELETE /api/secrets", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		q := r.URL.Query()
		s.record("secret-rm", map[string]any{
			"kind": q.Get("kind"), "addr": q.Get("addr"), "user": q.Get("user"), "key_path": q.Get("key_path"),
		})
		w.WriteHeader(http.StatusNoContent)
	})

	// --- page title ---
	mux.HandleFunc("GET /api/title", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		writeJSON(w, map[string]string{"title": s.title})
	})
	mux.HandleFunc("PUT /api/title", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		body := decode(r)
		title, _ := body["title"].(string)
		s.mu.Lock()
		s.title = title
		s.mu.Unlock()
		s.record("title", body)
		writeJSON(w, map[string]string{"title": title})
	})

	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func stubAPIClient(ts *apiStub) *apiClient {
	return &apiClient{base: ts.URL, token: testToken, http: ts.Client()}
}

// withStdin swaps os.Stdin for a pipe carrying content.
func withStdin(t *testing.T, content string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, content); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old })
}

func sampleHosts() []host.Host {
	return []host.Host{
		{ID: "h_1", Name: "prod", Address: "10.0.0.5", Port: 22, User: "root",
			Credential: host.Credential{Kind: host.CredKey, KeyPath: "~/.ssh/id_ed25519"}},
		{ID: "h_2", Name: "staging", Address: "10.0.0.6", Port: 2222, User: "ops",
			Credential: host.Credential{Kind: host.CredAgent},
			Forwards:   []host.Forward{{Kind: "local", Bind: "127.0.0.1:8080", Target: "localhost:80"}}},
	}
}
