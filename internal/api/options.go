package api

// Options configures the HTTP/WebSocket server.
type Options struct {
	Address     string `json:"address" flagName:"address" flagSName:"a" flagDescribe:"IP address to listen" default:"127.0.0.1"`
	Port        string `json:"port" flagName:"port" flagSName:"p" flagDescribe:"Port number to listen" default:"9049"`
	PermitWrite bool   `json:"permit_write" flagName:"permit-write" flagSName:"w" flagDescribe:"Permit clients to write to sessions (BE CAREFUL)" default:"true"`

	TitleFormat     string `json:"title_format" flagName:"title-format" flagDescribe:"Title format of browser window" default:"gossh - {{ .name }}@{{ .addr }}"`
	EnableReconnect bool   `json:"enable_reconnect" flagName:"reconnect" flagDescribe:"Enable reconnection" default:"true"`
	ReconnectTime   int    `json:"reconnect_time" flagName:"reconnect-time" flagDescribe:"Time to reconnect" default:"10"`

	MaxSession int `json:"max_session" flagName:"max-session" flagDescribe:"Maximum number of concurrent sessions (0 to disable)" default:"0"`

	// Mirror keeps a per-session VT-emulated screen grid (tee of the
	// session output) that powers the agent-driving API and answers
	// terminal queries when detached (so vim/htop do not hang).
	Mirror bool `json:"mirror" flagName:"mirror" flagDescribe:"Keep a screen mirror per session for the agent API (GET /screen, POST /wait)" default:"true"`

	AnswerQueries bool `json:"answer_queries" flagName:"answer-queries" flagDescribe:"Answer terminal queries when no browser client is attached" default:"true"`

	// Timeout 是会话淘汰策略:超过该秒数没有任何客户端附着(浏览器全关/
	// 断连)即销毁 SSH 会话;会话记录保留,可凭 id 重连。默认 900s。
	Timeout int `json:"timeout" flagName:"timeout" flagDescribe:"Idle timeout seconds for destroying unattached sessions (0 to disable)" default:"900"`

	// ConnectTimeout 是每个 SSH 跳的连接与握手超时(秒)。
	ConnectTimeout int `json:"connect_timeout" flagName:"connect-timeout" flagDescribe:"Seconds to wait for each SSH hop to connect and handshake" default:"10"`

	// SessionFile persists the session history (restart-safe). Empty disables it.
	SessionFile string `json:"session_file" flagName:"session-file" flagDescribe:"File path to persist session history (empty disables, default: ~/.gossh/sessions.json)" default:"~/.gossh/sessions.json"`

	// HostsFile persists the host inventory (single source of truth).
	HostsFile string `json:"hosts_file" flagName:"hosts-file" flagDescribe:"File path of the host inventory (default: ~/.gossh/hosts.json)" default:"~/.gossh/hosts.json"`

	// KnownHostsFile persists the TOFU host-key trust store.
	KnownHostsFile string `json:"known_hosts_file" flagName:"known-hosts-file" flagDescribe:"File path of the TOFU host-key store (default: ~/.gossh/known_hosts)" default:"~/.gossh/known_hosts"`

	// TokenFile persists the generated access token so restarts keep it.
	TokenFile string `json:"token_file" flagName:"token-file" flagDescribe:"File to persist the access token (empty = memory only)" default:"~/.gossh/token"`

	// Token is a fixed access token. Empty means "generate one at startup"
	// (persisted to TokenFile when set). The token gates every /api/*
	// request and the WebSocket endpoint.
	Token string `json:"token" flagName:"token" flagDescribe:"Access token required to open the page (empty = auto-generate and print)" default:""`

	// TitleFile persists the deployment-wide page title (browser tab title).
	TitleFile string `json:"title_file" flagName:"title-file" flagDescribe:"File path to persist the page title (empty disables, default: ~/.gossh/title.json)" default:"~/.gossh/title.json"`

	// LogFile writes the server log to a file (in addition to the console).
	LogFile string `json:"log_file" flagName:"log-file" flagDescribe:"Server log file path (empty = console only, default: ~/.gossh/logs/gossh.log)" default:"~/.gossh/logs/gossh.log"`

	Width    int    `json:"width" flagName:"width" flagDescribe:"Static width of the screen, 0(default) means dynamically resize" default:"0"`
	Height   int    `json:"height" flagName:"height" flagDescribe:"Static height of the screen, 0(default) means dynamically resize" default:"0"`
	WSOrigin string `json:"ws_origin" flagName:"ws-origin" flagDescribe:"A regular expression that matches origin URLs to be accepted by WebSocket" default:""`
	Term     string `json:"term" flagName:"term" flagDescribe:"Terminal name to use on the browser (xterm)" default:"xterm-256color"`

	EnableTLS  bool   `json:"enable_tls" flagName:"tls" flagSName:"t" flagDescribe:"Enable TLS/SSL" default:"false"`
	TLSCrtFile string `json:"tls_crt_file" flagName:"tls-crt" flagDescribe:"TLS/SSL certificate file path" default:"~/.gossh.crt"`
	TLSKeyFile string `json:"tls_key_file" flagName:"tls-key" flagDescribe:"TLS/SSL key file path" default:"~/.gossh.key"`

	// TitleVariables fills out the window title template (set by main).
	TitleVariables map[string]interface{} `json:"-"`
	// Preferences is sent to the client as a SetPreferences frame.
	Preferences map[string]interface{} `json:"preferences"`
}
