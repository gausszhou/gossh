//go:build run

package api

import "net/http"

// registerRunRoutes 注册单命令执行端点(仅 -tags run 构建启用;
// 默认构建不含,见 run_routes_disabled.go)。
func (server *Server) registerRunRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/run", server.handleRun)
}
