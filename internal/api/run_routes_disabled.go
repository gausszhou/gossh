//go:build !run

package api

import "net/http"

// registerRunRoutes 是单命令执行禁用态的桩:默认构建(无 -tags run)不注册
// POST /api/run,`gossh run` 子命令也不存在(Makefile RUN=1 启用)。
func (server *Server) registerRunRoutes(mux *http.ServeMux) {}
