package api

import (
	"encoding/json"
	"net/http"

	"github.com/gausszhou/gossh/internal/sshx"
)

// handleListKnownHosts implements GET /api/known-hosts — the TOFU pins
// (management UI: show fingerprints, delete a pin to retrust).
func (server *Server) handleListKnownHosts(w http.ResponseWriter, r *http.Request) {
	type pinView struct {
		Addr        string `json:"addr"`
		KeyType     string `json:"key_type"`
		Fingerprint string `json:"fingerprint"`
		FirstSeen   int64  `json:"first_seen"`
	}
	pins := server.knownHosts.Pins()
	views := make([]pinView, 0, len(pins))
	for addr, pin := range pins {
		views = append(views, pinView{
			Addr:        addr,
			KeyType:     pin.KeyType,
			Fingerprint: sshx.Fingerprint(pin.KeyType, pin.KeyBlob),
			FirstSeen:   pin.FirstSeen,
		})
	}
	writeJSON(w, http.StatusOK, views)
}

// handleForgetKnownHost implements DELETE /api/known-hosts/{addr}.
func (server *Server) handleForgetKnownHost(w http.ResponseWriter, r *http.Request) {
	addr := r.PathValue("addr")
	if err := server.knownHosts.Forget(addr); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to forget host key")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// secretRequest is the body of POST /api/secrets.
type secretRequest struct {
	Kind    string `json:"kind"` // "password" | "passphrase"
	Addr    string `json:"addr,omitempty"`
	User    string `json:"user,omitempty"`
	KeyPath string `json:"key_path,omitempty"`
	Secret  string `json:"secret"`
}

// handleSetSecret implements POST /api/secrets — save a password or key
// passphrase into the system keyring ("save to keyring" from the browser).
func (server *Server) handleSetSecret(w http.ResponseWriter, r *http.Request) {
	var req secretRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	switch req.Kind {
	case "password":
		if req.Addr == "" || req.User == "" {
			writeError(w, http.StatusBadRequest, "addr and user are required for a password")
			return
		}
		if err := server.secrets.SetPassword(req.Addr, req.User, req.Secret); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save password")
			return
		}
	case "passphrase":
		if req.KeyPath == "" {
			writeError(w, http.StatusBadRequest, "key_path is required for a passphrase")
			return
		}
		if err := server.secrets.SetPassphrase(req.KeyPath, req.Secret); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save passphrase")
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "kind must be password or passphrase")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"saved": true})
}

// handleDeleteSecret implements DELETE /api/secrets?kind=...&addr=...&user=...
// or ?kind=passphrase&key_path=...
func (server *Server) handleDeleteSecret(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	switch q.Get("kind") {
	case "password":
		if err := server.secrets.DeletePassword(q.Get("addr"), q.Get("user")); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to delete password")
			return
		}
	case "passphrase":
		if err := server.secrets.DeletePassphrase(q.Get("key_path")); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to delete passphrase")
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "kind must be password or passphrase")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
