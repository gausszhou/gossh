package api

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"sync"
	"syscall"

	"github.com/coder/websocket"

	"github.com/gausszhou/gossh/internal/session"
	"github.com/gausszhou/gossh/internal/terminal"
)

// signalByName maps a signal name to syscall.Signal.
func signalByName(name string) (syscall.Signal, bool) {
	signals := map[string]syscall.Signal{
		"SIGHUP":  syscall.SIGHUP,
		"SIGINT":  syscall.SIGINT,
		"SIGQUIT": syscall.SIGQUIT,
		"SIGKILL": syscall.SIGKILL,
		"SIGTERM": syscall.SIGTERM,
	}
	sig, ok := signals[name]
	return sig, ok
}

// handleWS implements GET /ws. It serves two protocols on the same endpoint:
//
//   - ?session_id=xxx — the legacy one-session-per-socket protocol, kept
//     during the transition (see docs/design/ws-multiplex.md);
//   - no session_id  — the multiplexed protocol, where one socket carries
//     any number of sessions and each frame names its session in a routing
//     header.
//
// Access control (token + ws-origin) is the same gate either way; it runs in
// the mux before this handler, so the protocol itself carries no credentials.
func (server *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if server.wsOriginMatcher != nil && !server.wsOriginMatcher.MatchString(r.Header.Get("Origin")) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols: terminal.Protocols,
	})
	if err != nil {
		log.Printf("Failed to accept websocket from %s: %s", r.RemoteAddr, err)
		return
	}
	// coder/websocket 默认消息读限 32KB,超限直接 1009 断连 —— 浏览器端
	// 一次粘贴/大块输入会整帧上行,很容易超限。调大到 16MB(仍有界,防滥
	// 用),配合 ReadMessage() 按完整消息解析(见 masterToSlave 帧读取)。
	conn.SetReadLimit(16 << 20)

	server.wsWG.Add(1)
	server.activeConns.Store(conn, struct{}{})
	defer func() {
		server.activeConns.Delete(conn)
		server.wsWG.Done()
		conn.CloseNow()
	}()

	log.Printf("New client connected: %s", r.RemoteAddr)

	if sid := r.URL.Query().Get("session_id"); sid != "" {
		server.handleWSSingle(conn, r, sid)
		return
	}
	server.handleWSMultiplexed(conn, r)
}

// handleWSSingle is the legacy protocol: the socket is bound to one session
// for its whole lifetime.
func (server *Server) handleWSSingle(conn *websocket.Conn, r *http.Request, sessionID string) {
	sess, err := server.manager.Get(sessionID)
	if err != nil {
		log.Printf("Session not found for %s: %s", r.RemoteAddr, sessionID)
		conn.Close(websocket.StatusPolicyViolation, "session not found")
		return
	}

	attachOpts := server.attachOptions(sess, r.RemoteAddr)

	adapter := &wsConn{conn: conn, ctx: r.Context()}
	attachErr := sess.Attach(r.Context(), adapter, attachOpts)

	closeReason := "unknown reason"
	switch attachErr {
	case nil:
		closeReason = "finished"
	case session.ErrClientClosed:
		closeReason = "client"
	case session.ErrSessionPreempted:
		closeReason = "preempted"
	case terminal.ErrTerminalClosed:
		closeReason = "terminal"
	default:
		closeReason = attachErr.Error()
	}
	log.Printf("Connection closed by %s: %s, reason: %s", closeReason, r.RemoteAddr, sess.ID())
}

// handleWSMultiplexed runs the multiplexed protocol: one socket, N sessions.
func (server *Server) handleWSMultiplexed(conn *websocket.Conn, r *http.Request) {
	newWSRouter(server, conn, r).serve()
}

// attachOptions builds the per-attach options shared by both protocols.
func (server *Server) attachOptions(sess *session.Session, remoteAddr string) session.AttachOptions {
	return session.AttachOptions{
		PermitWrite:      server.options.PermitWrite,
		FixedCols:        server.options.Width,
		FixedRows:        server.options.Height,
		WindowTitle:      server.attachWindowTitle(sess, remoteAddr),
		ReconnectSeconds: server.reconnectSeconds(),
		Preferences:      server.preferencesJSON(),
	}
}

// ---------------------------------------------------------------------------
// multiplexed routing
//
// wsRouter owns one WebSocket and demultiplexes it into one virtualConn per
// attached session. A virtualConn is an io.ReadWriter that Session.Attach
// accepts unchanged: the session layer keeps speaking plain [type][payload]
// frames, and the router adds (on write) or strips (on read) the routing
// header. See docs/design/ws-multiplex.md.
// ---------------------------------------------------------------------------

type wsRouter struct {
	server *Server
	conn   *websocket.Conn
	r      *http.Request
	ctx    context.Context

	mu    sync.Mutex
	chans map[string]*virtualConn

	// writeMu serializes conn.Write: a session bridge writes from its own
	// goroutine, so concurrent writes would interleave on the wire.
	writeMu sync.Mutex
}

func newWSRouter(server *Server, conn *websocket.Conn, r *http.Request) *wsRouter {
	return &wsRouter{
		server: server,
		conn:   conn,
		r:      r,
		ctx:    r.Context(),
		chans:  make(map[string]*virtualConn),
	}
}

// serve is the connection's read loop. It returns when the socket closes or
// fails, after which every channel on this connection is torn down.
func (rt *wsRouter) serve() {
	// 独立 ctx:handler 返回(r.Context() 取消)之前先关掉所有通道,让
	// 各 attach goroutine 有序退出,而不是等 context 取消被动打断。
	ctx, cancel := context.WithCancel(rt.ctx)
	rt.ctx = ctx
	defer func() {
		cancel()
		rt.closeAll()
	}()

	for {
		typ, reader, err := rt.conn.Reader(rt.ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageBinary {
			continue
		}
		msg, err := io.ReadAll(reader)
		if err != nil {
			return
		}

		frame, err := terminal.DecodeRouted(msg)
		if err != nil {
			// 一帧坏了不该拖累同一条连接上的其他会话:记日志后跳过。
			log.Printf("Dropping malformed multiplexed frame from %s: %s", rt.r.RemoteAddr, err)
			continue
		}
		rt.dispatch(frame)
	}
}

func (rt *wsRouter) dispatch(f terminal.RoutedFrame) {
	if f.IsConnLevel() {
		switch f.Type {
		case terminal.Ping:
			_ = rt.writeRaw(terminal.EncodeRoutedConn(terminal.Pong, nil))
		}
		return
	}

	switch f.Type {
	case terminal.Attach:
		// Attach blocks for the session's lifetime, so it runs per session.
		go rt.attach(f.SessionID)
	case terminal.Detach:
		rt.detach(f.SessionID)
	default:
		rt.channel(f.SessionID).deliver(append([]byte{f.Type}, f.Payload...))
	}
}

// attach binds a session to a fresh channel and pumps it until the attach
// ends (client gone, preempted, session destroyed, terminal closed).
func (rt *wsRouter) attach(sid string) {
	sess, err := rt.server.manager.Get(sid)
	if err != nil {
		_ = rt.writeRouted(sid, terminal.AttachFail, []byte("session not found"))
		return
	}

	vc := newVirtualConn(rt, sid, sess)
	rt.mu.Lock()
	if _, dup := rt.chans[sid]; dup {
		// 幂等:同一条连接重复 attach 同一会话,不产生第二次桥接。
		rt.mu.Unlock()
		_ = rt.writeRouted(sid, terminal.AttachOK, nil)
		return
	}
	rt.chans[sid] = vc
	rt.mu.Unlock()

	// AttachOK 与随后的初始化帧由同一 goroutine 顺序写出,客户端因此能
	// 先看到 OK 再看到输出。
	if err := rt.writeRouted(sid, terminal.AttachOK, nil); err != nil {
		rt.remove(sid, vc)
		return
	}

	attachErr := sess.Attach(rt.ctx, vc, rt.server.attachOptions(sess, rt.r.RemoteAddr))

	rt.remove(sid, vc)
	vc.closeLocal()

	switch attachErr {
	case nil, context.Canceled, context.DeadlineExceeded:
		log.Printf("Detached session %s from %s", sid, rt.r.RemoteAddr)
	case session.ErrSessionPreempted:
		// 被抢占:事件帧已由 virtualConn.Close 发出。
	case session.ErrSessionDestroyed:
		log.Printf("Session %s destroyed while attached to %s", sid, rt.r.RemoteAddr)
	case terminal.ErrTerminalClosed:
		log.Printf("Terminal closed for session %s (%s)", sid, rt.r.RemoteAddr)
	default:
		log.Printf("Attach to session %s from %s ended: %s", sid, rt.r.RemoteAddr, attachErr)
	}
}

// detach releases a channel because the client asked to (a view was closed).
// The session itself keeps running on the server.
func (rt *wsRouter) detach(sid string) {
	rt.mu.Lock()
	c := rt.chans[sid]
	delete(rt.chans, sid)
	rt.mu.Unlock()
	if c != nil {
		c.closeLocal()
	}
}

func (rt *wsRouter) channel(sid string) *virtualConn {
	if sid == "" {
		return nil
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.chans[sid]
}

// remove drops the channel from the map, but only if it is still the one
// registered for sid — a newer attach may already have replaced it.
func (rt *wsRouter) remove(sid string, c *virtualConn) {
	rt.mu.Lock()
	if cur, ok := rt.chans[sid]; ok && cur == c {
		delete(rt.chans, sid)
	}
	rt.mu.Unlock()
}

// closeAll tears down every channel when the connection goes away.
func (rt *wsRouter) closeAll() {
	rt.mu.Lock()
	chans := make([]*virtualConn, 0, len(rt.chans))
	for _, c := range rt.chans {
		chans = append(chans, c)
	}
	rt.chans = make(map[string]*virtualConn)
	rt.mu.Unlock()

	for _, c := range chans {
		c.closeLocal()
	}
}

func (rt *wsRouter) writeRouted(sid string, msgType byte, payload []byte) error {
	frame, err := terminal.EncodeRouted(sid, msgType, payload)
	if err != nil {
		return err
	}
	return rt.writeRaw(frame)
}

// writeRoutedSession wraps a plain session frame (b[0] is the type).
func (rt *wsRouter) writeRoutedSession(sid string, b []byte) error {
	frame, err := terminal.EncodeRoutedSession(sid, b)
	if err != nil {
		return err
	}
	return rt.writeRaw(frame)
}

func (rt *wsRouter) writeRaw(frame []byte) error {
	rt.writeMu.Lock()
	defer rt.writeMu.Unlock()
	return rt.conn.Write(rt.ctx, websocket.MessageBinary, frame)
}

// virtualConn is one session's logical channel over a shared WebSocket.
// It is what Session.Attach sees: an io.ReadWriter of [type][payload]
// frames, plus io.Closer so preemption can evict it, plus ReadMessage so
// masterToSlave keeps its frame-oriented read path.
type virtualConn struct {
	router *wsRouter
	sid    string
	sess   *session.Session

	mu     sync.Mutex
	cond   *sync.Cond
	queue  [][]byte
	closed bool
}

func newVirtualConn(rt *wsRouter, sid string, sess *session.Session) *virtualConn {
	c := &virtualConn{router: rt, sid: sid, sess: sess}
	c.cond = sync.NewCond(&c.mu)
	return c
}

// deliver queues one complete session-level frame for the bridge to read.
func (c *virtualConn) deliver(frame []byte) {
	if c == nil {
		return // no channel for this session: drop the stray frame
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.queue = append(c.queue, frame)
	c.mu.Unlock()
	c.cond.Broadcast()
}

// ReadMessage implements the frameReader interface: one call yields one
// complete [type][payload] frame, so a large paste is never split into
// bogus frames the way a byte-stream read would.
func (c *virtualConn) ReadMessage() ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for len(c.queue) == 0 && !c.closed {
		c.cond.Wait()
	}
	if len(c.queue) == 0 {
		return nil, io.EOF
	}
	frame := c.queue[0]
	c.queue[0] = nil
	c.queue = c.queue[1:]
	return frame, nil
}

// Read is the byte-stream view of the queue, kept so the type satisfies
// io.Reader for anything that reads without frame semantics.
func (c *virtualConn) Read(p []byte) (int, error) {
	frame, err := c.ReadMessage()
	if err != nil {
		return 0, err
	}
	return copy(p, frame), nil
}

func (c *virtualConn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if err := c.router.writeRoutedSession(c.sid, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Close is called by the session layer, which only does so when the
// attach is being evicted: preemption (a newer attach took over) or
// destruction. The client is told which, so it can show the right dialog
// instead of silently freezing.
func (c *virtualConn) Close() error {
	status := byte(terminal.EventPreempted)
	if c.sess != nil && c.sess.State() == session.StateDestroyed {
		status = terminal.EventDestroyed
	}
	c.closeLocal()
	_ = c.router.writeRouted(c.sid, terminal.SessionEvent, []byte{status})
	return nil
}

// closeLocal tears the channel down without notifying the client — used
// when the client itself detaches, or when the whole connection is gone.
func (c *virtualConn) closeLocal() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	c.cond.Broadcast()
	c.router.remove(c.sid, c)
}

// reconnectSeconds returns the reconnect delay for clients, 0 = disabled.
func (server *Server) reconnectSeconds() int {
	if server.options.EnableReconnect {
		return server.options.ReconnectTime
	}
	return 0
}

// preferencesJSON marshals the server preferences, nil when unset.
func (server *Server) preferencesJSON() []byte {
	if server.options.Preferences == nil {
		return nil
	}
	data, err := json.Marshal(server.options.Preferences)
	if err != nil {
		log.Printf("Failed to marshal preferences: %s", err)
		return nil
	}
	return data
}

// wsConn adapts a websocket.Conn into an io.ReadWriter over binary messages,
// where each binary message is treated as one protocol frame.
type wsConn struct {
	conn *websocket.Conn
	ctx  context.Context

	mu      sync.Mutex
	pending []byte
}

func (c *wsConn) Read(p []byte) (int, error) {
	for len(c.pending) == 0 {
		typ, reader, err := c.conn.Reader(c.ctx)
		if err != nil {
			return 0, err
		}
		if typ != websocket.MessageBinary {
			continue
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			return 0, err
		}
		c.pending = data
	}

	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

// ReadMessage returns one complete client message per call (frame-oriented).
// 与 Read 的"字节流"语义不同,它保证一条 WebSocket 消息(一帧)不会被打散:
// masterToSlave 据此把"一次 Read = 一帧"的假设建立在真实边界上 ——
// 超过单次 Read 缓冲(32KB)的输入帧(如大粘贴)不再被拆成两个假帧
// (第二个假帧会因帧首字节不是类型字节而被误判/误解析)。
func (c *wsConn) ReadMessage() ([]byte, error) {
	for {
		typ, reader, err := c.conn.Reader(c.ctx)
		if err != nil {
			return nil, err
		}
		if typ != websocket.MessageBinary {
			continue
		}
		return io.ReadAll(reader)
	}
}

func (c *wsConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.conn.Write(c.ctx, websocket.MessageBinary, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Close 实现 io.Closer:被同 id 的新 attach 抢占时由 Session 调用,
// 以 1013 关闭帧优雅告知旧客户端(浏览器据此显示"已被其他客户端接管")。
func (c *wsConn) Close() error {
	return c.conn.Close(websocket.StatusTryAgainLater, "session preempted by another client")
}
