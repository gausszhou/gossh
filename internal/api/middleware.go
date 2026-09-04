package api

import (
	"bufio"
	"context"
	"log"
	"net"
	"net/http"
)

// RunOptions holds a set of configurations for Server.Run().
type RunOptions struct {
	gracefullCtx context.Context
	urlHook      func(string)
}

// RunOption is an option of Server.Run().
type RunOption func(*RunOptions)

// WithGracefullContext accepts a context to shutdown a Server
// with care for existing client connections.
func WithGracefullContext(ctx context.Context) RunOption {
	return func(options *RunOptions) {
		options.gracefullCtx = ctx
	}
}

// WithURLHook 在监听器绑定成功后立即以浏览器可达的完整 UI URL
// (scheme://host:port/?token=...,host 规范化回环地址)调用 fn。
// serve 不用;app(桌面形态)用它拿到真实端口并交给浏览器/托盘。
func WithURLHook(fn func(url string)) RunOption {
	return func(options *RunOptions) {
		options.urlHook = fn
	}
}

func (server *Server) wrapLogger(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &logResponseWriter{w, 200}
		handler.ServeHTTP(rw, r)
		log.Printf("%s %d %s %s", r.RemoteAddr, rw.status, r.Method, r.URL.Path)
	})
}

func (server *Server) wrapHeaders(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// todo add version
		w.Header().Set("Server", "GoSSH")
		handler.ServeHTTP(w, r)
	})
}

type logResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *logResponseWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *logResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, _ := w.ResponseWriter.(http.Hijacker)
	w.status = http.StatusSwitchingProtocols
	return hj.Hijack()
}
