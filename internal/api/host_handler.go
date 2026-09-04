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

// handleCreateHost implements POST /api/hosts.
func (server *Server) handleCreateHost(w http.ResponseWriter, r *http.Request) {
	var h host.Host
	if err := json.NewDecoder(r.Body).Decode(&h); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if h.ID == "" {
		h.ID = host.NewID()
	}
	if err := server.inventory.Add(&h); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
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
	var h host.Host
	if err := json.NewDecoder(r.Body).Decode(&h); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	h.ID = r.PathValue("id")
	if err := server.inventory.Update(&h); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	log.Printf("Host updated: %s", h.Name)
	writeJSON(w, http.StatusOK, h)
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
