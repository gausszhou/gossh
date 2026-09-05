//go:build !sftp

package api

import "net/http"

// registerSFTPRoutes 是 SFTP 禁用态的桩:默认构建(无 -tags sftp)不注册
// 任何 SFTP 端点,二进制体积更小、面更小;启用见 Makefile 的 SFTP=1
// (等价于 go build -tags sftp)。
func (server *Server) registerSFTPRoutes(mux *http.ServeMux) {}
