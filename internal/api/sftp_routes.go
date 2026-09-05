//go:build sftp

package api

import "net/http"

// registerSFTPRoutes 注册会话级 SFTP 端点(仅 -tags sftp 构建启用;
// 默认构建不含 SFTP,见 sftp_routes_disabled.go)。
func (server *Server) registerSFTPRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sessions/{id}/sftp/ls", server.handleSFTPList)
	mux.HandleFunc("GET /api/sessions/{id}/sftp/stat", server.handleSFTPStat)
	mux.HandleFunc("POST /api/sessions/{id}/sftp/mkdir", server.handleSFTPMkdir)
	mux.HandleFunc("POST /api/sessions/{id}/sftp/rename", server.handleSFTPRename)
	mux.HandleFunc("POST /api/sessions/{id}/sftp/remove", server.handleSFTPRemove)
	mux.HandleFunc("GET /api/sessions/{id}/sftp/download", server.handleSFTPDownload)
	mux.HandleFunc("POST /api/sessions/{id}/sftp/upload", server.handleSFTPUpload)
}
