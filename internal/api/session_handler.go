package api

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"

	"github.com/gausszhou/gossh/internal/session"
	"github.com/gausszhou/gossh/internal/sshx"
	"github.com/gausszhou/gossh/internal/terminal"
	"github.com/gausszhou/gossh/internal/utils"
)

// savePasswordFor stores the password for the host's user@addr in the
// keyring (the browser "save to keyring" checkbox); 会话连接时调用。
func (server *Server) savePasswordFor(hostID, password string) error {
	h, err := server.inventory.Get(hostID)
	if err != nil {
		return err
	}
	return server.secrets.SetPassword(h.Addr(), h.User, password)
}

// forwardProvidedSecrets 把本次会话请求里浏览器输入的密码/口令构造成
// 转发连接拨号凭据(仅本次有效,不入 keyring;未提供则为空)。
func forwardProvidedSecrets(req createSessionRequest) *sshx.ProvidedSecrets {
	prov := &sshx.ProvidedSecrets{}
	if req.Password != nil && *req.Password != "" {
		prov.Password = req.Password
	}
	if req.Passphrase != nil && *req.Passphrase != "" {
		prov.Passphrase = req.Passphrase
	}
	return prov
}

// Rest API — session management.
// 列表由客户端清单(localStorage)驱动;服务端只按 id 提供:
// 创建(幂等/复活)、详情、状态批量查询、销毁、重命名、resize/signal。

type createSessionRequest struct {
	ID string `json:"id"` // client-chosen 16 base36 chars (optional)

	// HostID is the inventory host this session connects to. It is
	// required for fresh sessions; resurrection uses the recorded spec.
	HostID string `json:"host_id"`

	// Password/Passphrase are per-connect secrets for THIS attempt only
	// (never persisted; the *_save flags move them into the keyring
	// after a successful connect).
	Password       *string `json:"password,omitempty"`
	Passphrase     *string `json:"passphrase,omitempty"`
	SavePassword   bool    `json:"save_password,omitempty"`
	SavePassphrase bool    `json:"save_passphrase,omitempty"`
}

type sessionStatusResponse struct {
	// Sessions keyed by id, alive ones only.
	Sessions map[string]session.StateDescription `json:"sessions"`
}

// handleCreateSession implements POST /api/sessions.
// A client-chosen id (16 base36 chars) makes the call idempotent
// (alive → existing session) or resurrect the recorded session
// (record → rebuild with the recorded host, run_count+1).
// Without an id the server generates one.
func (server *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req createSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.ID != "" && !utils.IsValidSessionID(req.ID) {
		writeError(w, http.StatusBadRequest, "invalid session id: must be 16 base36 characters")
		return
	}

	spec := session.ConnectSpec{HostID: req.HostID}
	if req.HostID != "" {
		if h, err := server.inventory.Get(req.HostID); err == nil {
			spec.Name = h.Name
			spec.Addr = h.Addr()
			spec.User = h.User
		} else {
			writeError(w, http.StatusBadRequest, "unknown host_id")
			return
		}
	}

	var termOpts []terminal.Option
	if req.Password != nil || req.Passphrase != nil {
		termOpts = append(termOpts, terminal.WithDialCredentials(&terminal.DialCredentials{
			Password:   deref(req.Password),
			Passphrase: deref(req.Passphrase),
		}))
	}

	sess, created, err := server.manager.CreateWithID(req.ID, spec, termOpts...)
	if err != nil {
		switch err {
		case session.ErrTooManySessions:
			writeError(w, http.StatusServiceUnavailable, "too many sessions")
		case session.ErrNoHost:
			writeError(w, http.StatusBadRequest, "no host given")
		default:
			log.Printf("Failed to create session: %s", err)
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	// 主机级转发常驻在主机专属转发连接上(ADR-0007):每次会话建立都顺带
	// ensure(幂等)——连接未起则带本次凭据拨号并拉起 host.Forwards;
	// 浏览器输入的密码/口令顺带成为转发连接的拨号凭据。转发不随会话销毁。
	if spec.HostID != "" {
		server.forwardHosts.ensure(spec.HostID, forwardProvidedSecrets(req))
	}

	// 连接成功后按需把秘密存入 keyring
	if req.SavePassword && req.Password != nil && *req.Password != "" {
		if hpErr := server.savePasswordFor(req.HostID, *req.Password); hpErr != nil {
			log.Printf("Failed to save password to keyring: %s", hpErr)
		}
	}
	if req.SavePassphrase && req.Passphrase != nil && *req.Passphrase != "" {
		if h, hpErr := server.inventory.Get(req.HostID); hpErr == nil && h.Credential.Kind == "key" {
			if seErr := server.secrets.SetPassphrase(h.Credential.KeyPath, *req.Passphrase); seErr != nil {
				log.Printf("Failed to save passphrase to keyring: %s", seErr)
			}
		}
	}

	status := http.StatusCreated
	if !created {
		// 幂等命中已有会话
		status = http.StatusOK
	}
	log.Printf("Session created: %s -> %s (%s)", sess.ID(), spec.Name, spec.Addr)
	writeJSON(w, status, sess.StateDescription())
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// handleSessionStatus implements POST /api/sessions/status.
// The client manifest polls this to learn which of its ids are alive.
func (server *Server) handleSessionStatus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	resp := sessionStatusResponse{
		Sessions: map[string]session.StateDescription{},
	}
	for _, sess := range server.manager.Status(req.IDs) {
		resp.Sessions[sess.ID()] = sess.StateDescription()
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleUpdateTitle implements PUT /api/sessions/{id}/title
// (persisted on the server; works for alive and historical sessions).
func (server *Server) handleUpdateTitle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := server.manager.UpdateTitle(r.PathValue("id"), req.Title); err != nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"title": req.Title})
}

// handleGetSession implements GET /api/sessions/{id}.
func (server *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	sess, err := server.manager.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	writeJSON(w, http.StatusOK, sess.StateDescription())
}

// handleDeleteSession implements DELETE /api/sessions/{id}.
func (server *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := server.manager.Destroy(id); err != nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	// 销毁会话时一并关闭其端口转发
	for _, e := range server.forwards.list(id) {
		if e.cancel != nil {
			e.cancel()
		}
		server.forwards.remove(id, e.ID)
	}
	log.Printf("Session destroyed: %s", id)
	w.WriteHeader(http.StatusNoContent)
}

// handleResizeSession implements POST /api/sessions/{id}/resize.
func (server *Server) handleResizeSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Width <= 0 || req.Height <= 0 {
		writeError(w, http.StatusBadRequest, "width and height must be positive")
		return
	}

	sess, err := server.manager.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	if err := sess.Resize(req.Width, req.Height); err != nil {
		writeError(w, http.StatusConflict, "session is not resizable")
		return
	}
	writeJSON(w, http.StatusOK, sess.StateDescription())
}

// handleSignalSession implements POST /api/sessions/{id}/signal.
func (server *Server) handleSignalSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Signal string `json:"signal"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	sig, ok := signalByName(req.Signal)
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown signal: "+req.Signal)
		return
	}

	sess, err := server.manager.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	if err := sess.Signal(sig); err != nil {
		writeError(w, http.StatusConflict, "failed to send signal")
		return
	}
	writeJSON(w, http.StatusOK, sess.StateDescription())
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
