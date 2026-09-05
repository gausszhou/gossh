package api

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"regexp"
	"sync"
	noesctmpl "text/template"
	"time"

	"github.com/NYTimes/gziphandler"
	"github.com/coder/websocket"

	"github.com/gausszhou/gossh/internal/host"
	"github.com/gausszhou/gossh/internal/session"
	"github.com/gausszhou/gossh/internal/sshtty"
	"github.com/gausszhou/gossh/internal/sshx"
	"github.com/gausszhou/gossh/internal/terminal"
	"github.com/gausszhou/gossh/internal/utils"
)

// Server provides the HTTP REST API, the WebSocket attach endpoint, the
// host inventory API and the static terminal page.
type Server struct {
	manager    *session.Manager
	options    *Options
	inventory  *host.Inventory
	knownHosts *sshx.KnownHosts
	secrets    *sshx.Secrets

	token string

	titleTemplate *noesctmpl.Template
	titleStore    *TitleStore

	wsOriginMatcher *regexp.Regexp

	forwards *ForwardRegistry

	// forwardHosts 管理主机级转发的独立连接与运行时(ADR-0007)
	forwardHosts *ForwardHostManager

	activeConns sync.Map // *websocket.Conn -> struct{}
	wsWG        sync.WaitGroup
}

// New creates a Server. It injects the session manager's terminal
// factory (dialing gossh-style SSH connections), validates the embedded
// index page and parses the window title template.
func New(manager *session.Manager, options *Options, inventory *host.Inventory, knownHosts *sshx.KnownHosts, secrets *sshx.Secrets) (*Server, error) {
	if _, err := fs.ReadFile(staticFiles, "static/index.html"); err != nil {
		return nil, fmt.Errorf("index page not found in embedded static files: %w", err)
	}

	titleTemplate, err := noesctmpl.New("title").Parse(options.TitleFormat)
	if err != nil {
		return nil, fmt.Errorf("failed to parse window title format `%s`: %w", options.TitleFormat, err)
	}

	var originMatcher *regexp.Regexp
	if options.WSOrigin != "" {
		matcher, err := regexp.Compile(options.WSOrigin)
		if err != nil {
			return nil, fmt.Errorf("failed to compile regular expression of Websocket Origin `%s`: %w", options.WSOrigin, err)
		}
		originMatcher = matcher
	}

	token, err := ResolveToken(options)
	if err != nil {
		return nil, err
	}

	server := &Server{
		manager:         manager,
		options:         options,
		inventory:       inventory,
		knownHosts:      knownHosts,
		secrets:         secrets,
		token:           token,
		titleTemplate:   titleTemplate,
		titleStore:      NewTitleStore(options.TitleFile),
		wsOriginMatcher: originMatcher,
		forwards:        NewForwardRegistry(),
	}
	// 主机级转发管理器:转发挂在主机专属转发连接上,不随会话生灭(ADR-0007)
	server.forwardHosts = NewForwardHostManager(
		server.dialHostForward,
		server.launchOnClient,
		func(hostID string) []host.Forward {
			h, err := server.inventory.Get(hostID)
			if err != nil || h == nil {
				return nil
			}
			return h.Forwards
		},
	)
	manager.WithTerminalFactory(server.dialFactory)
	return server, nil
}

// dialHostForward 建立主机级转发连接:与 session 拨号同一条链路
// (连接链 + TOFU + 凭据解析),但不开 PTY——连接只承载端口转发。
func (server *Server) dialHostForward(hostID string, prov *sshx.ProvidedSecrets) (*sshx.DialResult, error) {
	chain, err := server.inventory.Chain(hostID)
	if err != nil {
		return nil, err
	}
	hops, err := sshx.BuildHops(chain, server.secrets, prov, server.knownHosts, server.connectTimeout())
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), server.chainTimeout(len(hops)))
	defer cancel()
	return sshx.DialChain(ctx, hops)
}

// Token returns the effective access token (used by the CLI to print
// the login URL).
func (server *Server) Token() string { return server.token }

// dialFactory is the session manager's TerminalFactory: resolve the host
// chain, dial it hop by hop with TOFU host-key checks, and wrap the
// connection in an ssh tty.
func (server *Server) dialFactory(spec session.ConnectSpec, opts ...terminal.Option) (session.Terminal, error) {
	h, err := server.inventory.Get(spec.HostID)
	if err != nil {
		return nil, fmt.Errorf("host not found: %w", err)
	}
	chain, err := server.inventory.Chain(spec.HostID)
	if err != nil {
		return nil, err
	}

	prov := &sshx.ProvidedSecrets{}
	o := terminal.Apply(opts...)
	if o.DialCredentials != nil {
		if o.DialCredentials.Password != "" {
			prov.Password = &o.DialCredentials.Password
		}
		if o.DialCredentials.Passphrase != "" {
			prov.Passphrase = &o.DialCredentials.Passphrase
		}
	}

	hops, err := sshx.BuildHops(chain, server.secrets, prov, server.knownHosts, server.connectTimeout())
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), server.chainTimeout(len(hops)))
	defer cancel()
	dial, err := sshx.DialChain(ctx, hops)
	if err != nil {
		return nil, err
	}

	tty, err := sshtty.New(dial, h, opts...)
	if err != nil {
		_ = dial.Close()
		return nil, fmt.Errorf("failed to open ssh shell: %w", err)
	}
	return tty, nil
}

func (server *Server) connectTimeout() time.Duration {
	d := time.Duration(server.options.ConnectTimeout) * time.Second
	if d <= 0 {
		d = 10 * time.Second
	}
	return d
}

func (server *Server) chainTimeout(hops int) time.Duration {
	return server.connectTimeout()*time.Duration(hops) + 5*time.Second
}

// Run starts the HTTP server. It blocks until ctx is canceled.
func (server *Server) Run(ctx context.Context, options ...RunOption) error {
	opts := &RunOptions{gracefullCtx: context.Background()}
	for _, opt := range options {
		opt(opts)
	}

	server.manager.Start(ctx)

	srv := &http.Server{Handler: server.setupHandlers()}

	if server.options.PermitWrite {
		log.Printf("Permitting clients to write input to the sessions.")
	}
	if server.options.Port == "0" {
		log.Printf("Port number configured to `0`, choosing a random port")
	}

	hostPort := net.JoinHostPort(server.options.Address, server.options.Port)
	listener, err := net.Listen("tcp", hostPort)
	if err != nil {
		return fmt.Errorf("failed to listen at `%s`: %w", hostPort, err)
	}

	scheme := "http"
	if server.options.EnableTLS {
		scheme = "https"
	}
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	log.Printf("HTTP server is listening at: %s", scheme+"://"+host+":"+port)
	log.Printf("Open the page with the access token:")
	log.Printf("  %s://%s:%s/?token=%s", scheme, host, port, server.token)
	// 桌面形态(app)在绑定后就拿到浏览器 URL(回环规范化,0.0.0.0 等通配
	// 地址对浏览器不可达,统一换成 127.0.0.1)
	if opts.urlHook != nil {
		browserHost := host
		if browserHost == "0.0.0.0" || browserHost == "::" || browserHost == "" {
			browserHost = "127.0.0.1"
		}
		opts.urlHook(fmt.Sprintf("%s://%s:%s/?token=%s", scheme, browserHost, port, server.token))
	}
	if server.options.Address == "0.0.0.0" || server.options.Address == "::" {
		for _, address := range listAddresses() {
			log.Printf("Alternative URL: %s://%s:%s/?token=%s", scheme, address, port, server.token)
		}
	}

	srvErr := make(chan error, 1)
	go func() {
		if server.options.EnableTLS {
			crtFile := utils.Expand(server.options.TLSCrtFile)
			keyFile := utils.Expand(server.options.TLSKeyFile)
			log.Printf("TLS crt file: %s", crtFile)
			log.Printf("TLS key file: %s", keyFile)
			err = srv.ServeTLS(listener, crtFile, keyFile)
		} else {
			err = srv.Serve(listener)
		}
		if err != nil {
			srvErr <- err
		}
	}()

	// Graceful shutdown: drain request handlers, then force-close
	// hijacked WebSocket connections so that attach loops unwind.
	go func() {
		select {
		case <-opts.gracefullCtx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = srv.Shutdown(shutdownCtx)
			cancel()
			server.activeConns.Range(func(key, _ interface{}) bool {
				conn := key.(*websocket.Conn)
				_ = conn.Close(websocket.StatusGoingAway, "server shutting down")
				return true
			})
			server.wsWG.Wait()
		case <-ctx.Done():
			_ = srv.Close()
		}
	}()

	select {
	case err = <-srvErr:
		if err == http.ErrServerClosed { // by graceful ctx
			err = nil
		} else {
			log.Printf("HTTP server error: %s", err)
		}
	case <-ctx.Done():
		_ = srv.Close()
		err = ctx.Err()
	}

	// 释放全部主机级转发连接(端口监听随之后台进程退出关闭)
	server.forwardHosts.closeAll()

	return err
}

// setupHandlers wires the route table. /api/* and /ws require the access
// token; the static page itself is served without it (the frontend reads
// the token from the URL and attaches it to API calls).
func (server *Server) setupHandlers() http.Handler {
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic("failed to create static filesystem sub-root")
	}
	staticFileHandler := http.FileServer(http.FS(staticFS))

	apiMux := http.NewServeMux()

	// REST API — sessions (create/host_id semantics, see session_handler.go)
	apiMux.HandleFunc("POST /api/sessions", server.handleCreateSession)
	apiMux.HandleFunc("POST /api/sessions/status", server.handleSessionStatus)
	apiMux.HandleFunc("GET /api/sessions/{id}", server.handleGetSession)
	apiMux.HandleFunc("PUT /api/sessions/{id}/title", server.handleUpdateTitle)
	apiMux.HandleFunc("DELETE /api/sessions/{id}", server.handleDeleteSession)
	apiMux.HandleFunc("POST /api/sessions/{id}/resize", server.handleResizeSession)
	apiMux.HandleFunc("POST /api/sessions/{id}/signal", server.handleSignalSession)

	// REST API — agent driving (backed by the screen mirror)
	apiMux.HandleFunc("GET /api/sessions/{id}/screen", server.handleGetScreen)
	apiMux.HandleFunc("POST /api/sessions/{id}/wait", server.handleWaitSession)
	apiMux.HandleFunc("POST /api/sessions/{id}/keys", server.handleKeys)

	// REST API — host inventory
	apiMux.HandleFunc("GET /api/hosts", server.handleListHosts)
	apiMux.HandleFunc("POST /api/hosts", server.handleCreateHost)
	apiMux.HandleFunc("GET /api/hosts/{id}", server.handleGetHost)
	apiMux.HandleFunc("PUT /api/hosts/{id}", server.handleUpdateHost)
	apiMux.HandleFunc("DELETE /api/hosts/{id}", server.handleDeleteHost)
	apiMux.HandleFunc("GET /api/hosts/{id}/parents", server.handleHostParents)
	apiMux.HandleFunc("GET /api/hosts/{id}/forwards", server.handleListHostForwards)

	// REST API — TOFU trust store management
	apiMux.HandleFunc("GET /api/known-hosts", server.handleListKnownHosts)
	apiMux.HandleFunc("DELETE /api/known-hosts/{addr}", server.handleForgetKnownHost)

	// REST API — secrets (system keyring)
	apiMux.HandleFunc("POST /api/secrets", server.handleSetSecret)
	apiMux.HandleFunc("DELETE /api/secrets", server.handleDeleteSecret)

	// REST API — session-scoped SFTP (同一会话连接;默认构建不含,SFTP=1 开启)
	server.registerSFTPRoutes(apiMux)

	// REST API — session-scoped port forwards
	apiMux.HandleFunc("GET /api/sessions/{id}/forwards", server.handleListForwards)
	apiMux.HandleFunc("POST /api/sessions/{id}/forwards", server.handleAddForward)
	apiMux.HandleFunc("DELETE /api/sessions/{id}/forwards/{fid}", server.handleDeleteForward)

	// REST API — deployment-wide page title
	apiMux.HandleFunc("GET /api/title", server.handleGetTitle)
	apiMux.HandleFunc("PUT /api/title", server.handlePutTitle)

	requireToken := server.requireToken

	var siteHandler http.Handler = apiMux
	siteHandler = requireToken(siteHandler)
	siteHandler = gziphandler.GzipHandler(server.wrapHeaders(siteHandler))
	siteHandler = server.wrapLogger(siteHandler)

	mux := http.NewServeMux()
	// 静态页与静态资源不需要令牌(前端自行携带令牌调 API)
	mux.Handle("GET /{$}", server.wrapLogger(http.HandlerFunc(server.handleIndex)))
	mux.Handle("GET /main.js", http.StripPrefix("/", staticFileHandler))
	mux.Handle("GET /favicon.png", http.StripPrefix("/", staticFileHandler))
	// /api/* 与 /ws 全量要求令牌
	mux.Handle("/api/", siteHandler)
	mux.Handle("GET /ws", server.wrapLogger(requireToken(http.HandlerFunc(server.handleWS))))

	return mux
}

// SetupHandlers returns the full route handler (site + REST + WS).
// Exported for tests that run a gossh server inside the same process.
func (server *Server) SetupHandlers() http.Handler {
	return server.setupHandlers()
}

// handleIndex serves the terminal page (vite build 产物,直接透传)。
func (server *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	content, err := fs.ReadFile(staticFiles, "static/index.html")
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Write(content)
}

// attachWindowTitle renders the window title for an attached session.
func (server *Server) attachWindowTitle(sess *session.Session, remoteAddr string) []byte {
	spec := sess.Spec()
	titleVars := server.titleVariables(
		[]string{"server", "master", "session"},
		map[string]map[string]interface{}{
			"server": server.options.TitleVariables,
			"master": {
				"remote_addr": remoteAddr,
			},
			"session": {
				"id":   sess.ID(),
				"name": spec.Name,
				"addr": spec.Addr,
				"user": spec.User,
				"host": spec.Name,
			},
		},
	)

	titleBuf := new(bytes.Buffer)
	if err := server.titleTemplate.Execute(titleBuf, titleVars); err != nil {
		log.Printf("Failed to fill window title template: %s", err)
		return nil
	}
	return titleBuf.Bytes()
}

// titleVariables merges maps in a specified order.
func (server *Server) titleVariables(order []string, varUnits map[string]map[string]interface{}) map[string]interface{} {
	titleVars := map[string]interface{}{}

	for _, name := range order {
		vars, ok := varUnits[name]
		if !ok {
			panic("title variable name error")
		}
		for key, val := range vars {
			titleVars[key] = val
		}
	}

	// safe net for conflicted keys
	for _, name := range order {
		titleVars[name] = varUnits[name]
	}

	return titleVars
}

// listAddresses enumerates the addresses of all network interfaces.
func listAddresses() (addresses []string) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return []string{}
	}

	addresses = make([]string, 0, len(ifaces))
	for _, iface := range ifaces {
		ifAddrs, _ := iface.Addrs()
		for _, ifAddr := range ifAddrs {
			switch v := ifAddr.(type) {
			case *net.IPNet:
				addresses = append(addresses, v.IP.String())
			case *net.IPAddr:
				addresses = append(addresses, v.IP.String())
			}
		}
	}
	return addresses
}
