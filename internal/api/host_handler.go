package api

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/gausszhou/gossh/internal/host"
)

// handleListHosts implements GET /api/hosts.
func (server *Server) handleListHosts(w http.ResponseWriter, r *http.Request) {
	hosts := server.inventory.List()

	// 附上每个主机的跳板路径,前端可直接渲染连接链
	type hostView struct {
		*host.Host
		ViaNames []string `json:"via_names"`
	}
	views := make([]hostView, 0, len(hosts))
	for _, h := range hosts {
		v := hostView{Host: h}
		if parents, err := server.inventory.Parents(h.ID); err == nil {
			for _, pid := range parents {
				if p, err := server.inventory.Get(pid); err == nil {
					v.ViaNames = append(v.ViaNames, p.Name)
				}
			}
		}
		views = append(views, v)
	}
	writeJSON(w, http.StatusOK, views)
}

// hostRequest 是 POST/PUT /api/hosts 的请求体:主机字段平铺,
// 外加一次性的密码写入(keyring)。密码绝不进 hosts.json;
// save_password=true 时在保存主机后把密码写入系统 keyring。
type hostRequest struct {
	host.Host
	Password     *string `json:"password,omitempty"`
	SavePassword bool    `json:"save_password,omitempty"`
}

// handleCreateHost implements POST /api/hosts.
func (server *Server) handleCreateHost(w http.ResponseWriter, r *http.Request) {
	var req hostRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	h := req.Host
	if h.ID == "" {
		h.ID = host.NewID()
	}
	if err := server.inventory.Add(&h); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	server.saveHostPassword(&req, &h)
	log.Printf("Host added: %s (%s@%s)", h.Name, h.User, h.Addr())
	writeJSON(w, http.StatusCreated, h)
}

// handleGetHost implements GET /api/hosts/{id}.
func (server *Server) handleGetHost(w http.ResponseWriter, r *http.Request) {
	h, err := server.inventory.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "host not found")
		return
	}
	writeJSON(w, http.StatusOK, h)
}

// handleUpdateHost implements PUT /api/hosts/{id} (full replace).
func (server *Server) handleUpdateHost(w http.ResponseWriter, r *http.Request) {
	var req hostRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	h := req.Host
	h.ID = r.PathValue("id")
	if err := server.inventory.Update(&h); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	server.saveHostPassword(&req, &h)
	log.Printf("Host updated: %s", h.Name)
	writeJSON(w, http.StatusOK, h)
}

// saveHostPassword 在请求携带密码且勾选保存时,把密码写入系统 keyring
// (按 user@addr 键控,与连接时的 keyring 查询一致)。明文不进 hosts.json。
func (server *Server) saveHostPassword(req *hostRequest, h *host.Host) {
	if req == nil || req.Password == nil || *req.Password == "" || !req.SavePassword {
		return
	}
	if err := server.secrets.SetPassword(h.Addr(), h.User, *req.Password); err != nil {
		log.Printf("Failed to save password to keyring for %s@%s: %s", h.User, h.Addr(), err)
		return
	}
	log.Printf("Password saved to keyring for %s@%s", h.User, h.Addr())
}

// handleDeleteHost implements DELETE /api/hosts/{id}.
func (server *Server) handleDeleteHost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := server.inventory.Remove(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	log.Printf("Host removed: %s", id)
	w.WriteHeader(http.StatusNoContent)
}

// handleHostParents implements GET /api/hosts/{id}/parents — the jump
// path (nearest first) for the UI.
func (server *Server) handleHostParents(w http.ResponseWriter, r *http.Request) {
	parents, err := server.inventory.Parents(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "host not found")
		return
	}
	writeJSON(w, http.StatusOK, parents)
}
