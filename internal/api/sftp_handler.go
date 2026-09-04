package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/pkg/sftp"
)

// sftpEntry is the wire form of one remote directory entry.
type sftpEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	Mode    string `json:"mode"`
	IsDir   bool   `json:"is_dir"`
	IsLink  bool   `json:"is_link"`
	ModTime int64  `json:"mod_time"`
}

// sftpClientFor opens a fresh SFTP client on the session's ssh connection.
func (server *Server) sftpClientFor(w http.ResponseWriter, r *http.Request) (*sftp.Client, bool) {
	sess, err := server.manager.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "session not found")
		return nil, false
	}
	client, err := sess.SSHClient()
	if err != nil {
		writeError(w, http.StatusConflict, "session has no ssh connection")
		return nil, false
	}
	sc, err := sftp.NewClient(client)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to open sftp channel")
		return nil, false
	}
	return sc, true
}

func entryFromInfo(path string, info os.FileInfo) sftpEntry {
	return sftpEntry{
		Name:    info.Name(),
		Path:    path,
		Size:    info.Size(),
		Mode:    info.Mode().String(),
		IsDir:   info.IsDir(),
		IsLink:  info.Mode()&os.ModeSymlink != 0,
		ModTime: info.ModTime().Unix(),
	}
}

// handleSFTPList implements GET /api/sessions/{id}/sftp/ls?path=...
func (server *Server) handleSFTPList(w http.ResponseWriter, r *http.Request) {
	sc, ok := server.sftpClientFor(w, r)
	if !ok {
		return
	}
	defer sc.Close()

	path := r.URL.Query().Get("path")
	if path == "" {
		path = "."
	}
	entries, err := sc.ReadDir(path)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to list `%s`: %v", path, err))
		return
	}
	out := make([]sftpEntry, 0, len(entries))
	for _, e := range entries {
		child := strings.TrimSuffix(path, "/") + "/" + e.Name()
		out = append(out, entryFromInfo(child, e))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleSFTPStat implements GET /api/sessions/{id}/sftp/stat?path=...
func (server *Server) handleSFTPStat(w http.ResponseWriter, r *http.Request) {
	sc, ok := server.sftpClientFor(w, r)
	if !ok {
		return
	}
	defer sc.Close()

	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	info, err := sc.Stat(path)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to stat `%s`: %v", path, err))
		return
	}
	writeJSON(w, http.StatusOK, entryFromInfo(path, info))
}

// handleSFTPMkdir implements POST /api/sessions/{id}/sftp/mkdir {"path"}.
func (server *Server) handleSFTPMkdir(w http.ResponseWriter, r *http.Request) {
	sc, ok := server.sftpClientFor(w, r)
	if !ok {
		return
	}
	defer sc.Close()

	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	if err := sc.MkdirAll(req.Path); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to mkdir `%s`: %v", req.Path, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"created": true})
}

// handleSFTPRename implements POST /api/sessions/{id}/sftp/rename {"from","to"}.
func (server *Server) handleSFTPRename(w http.ResponseWriter, r *http.Request) {
	sc, ok := server.sftpClientFor(w, r)
	if !ok {
		return
	}
	defer sc.Close()

	var req struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.From == "" || req.To == "" {
		writeError(w, http.StatusBadRequest, "from and to are required")
		return
	}
	if err := sc.Rename(req.From, req.To); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to rename: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"renamed": true})
}

// handleSFTPRemove implements POST /api/sessions/{id}/sftp/remove {"path"}.
func (server *Server) handleSFTPRemove(w http.ResponseWriter, r *http.Request) {
	sc, ok := server.sftpClientFor(w, r)
	if !ok {
		return
	}
	defer sc.Close()

	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	info, err := sc.Stat(req.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to stat `%s`: %v", req.Path, err))
		return
	}
	if info.IsDir() {
		err = sc.RemoveDirectory(req.Path)
	} else {
		err = sc.Remove(req.Path)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to remove `%s`: %v", req.Path, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"removed": true})
}

// handleSFTPDownload implements GET /api/sessions/{id}/sftp/download?path=...
// — streams the remote file to the browser.
func (server *Server) handleSFTPDownload(w http.ResponseWriter, r *http.Request) {
	sc, ok := server.sftpClientFor(w, r)
	if !ok {
		return
	}
	defer sc.Close()

	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	src, err := sc.Open(path)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to open `%s`: %v", path, err))
		return
	}
	defer src.Close()

	info, _ := src.Stat()
	base := path
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", base))
	if info != nil {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	}
	_, _ = io.Copy(w, src)
}

// handleSFTPUpload implements POST /api/sessions/{id}/sftp/upload?path=...
// — raw request body is written to the remote path.
func (server *Server) handleSFTPUpload(w http.ResponseWriter, r *http.Request) {
	sc, ok := server.sftpClientFor(w, r)
	if !ok {
		return
	}
	defer sc.Close()

	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	dst, err := sc.Create(path)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to create `%s`: %v", path, err))
		return
	}
	defer dst.Close()

	n, err := io.Copy(dst, r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("upload failed: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"written": n})
}
