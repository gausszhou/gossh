package cmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/gausszhou/gossh/internal/api"
	"github.com/gausszhou/gossh/internal/config"
	"github.com/gausszhou/gossh/internal/host"
	"github.com/gausszhou/gossh/internal/session"
)

// `gossh session` is the command-line face of the agent-driving API
// (GET /screen, POST /wait, POST /keys — internal/api/agent_handler.go).
//
// The design follows terminal-use (MIT, https://github.com/flipbit03/terminal-use):
// a stateless, one-shot CLI over a long-running owner of the sessions. In tu
// that owner is its own daemon; here it is the `gossh serve` / `gossh app`
// process that is already running, so this group is a thin HTTP client and
// never opens a PTY of its own. Two conventions are borrowed verbatim:
//
//   - output is human-readable on a TTY and JSON when piped, with --json to
//     force either (see cliJSON);
//   - keys are named on the command line ("Enter", "Ctrl+C", "F5") and
//     translated client-side (cmd/keys.go), because the server deliberately
//     takes raw bytes.

func buildSessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Drive sessions from the command line (the agent-driving API)",
		Long: "Drive the sessions of a running gossh server from the command line.\n\n" +
			"Every subcommand talks to the same REST API the browser uses, and the\n" +
			"sessions live in that server — this CLI is stateless and one-shot.\n\n" +
			"The address and access token come from the same config file\n" +
			"`gossh serve` reads (default ~/.gossh/config.json), so a stock setup\n" +
			"needs no flags; --server / --token (or GOSSH_SERVER / GOSSH_TOKEN)\n" +
			"override them.\n\n" +
			"Reading a screen needs the server's screen mirror (--mirror, on by\n" +
			"default); writing keys needs --permit-write (also on by default).",
	}
	cmd.PersistentFlags().String("server", "", "Base URL of the running server (default: derived from the config file)")
	cmd.PersistentFlags().String("token", "", "Access token (default: GOSSH_TOKEN, config file, then the token file)")
	cmd.PersistentFlags().String("token-file", "", "File holding the access token (default: the config file's token_file)")
	cmd.PersistentFlags().StringP("session", "s", "", "Target session: id, unique id prefix, or title (default: the only session)")
	cmd.PersistentFlags().Bool("json", false, "Emit JSON (default: human-readable on a TTY, JSON when piped)")

	cmd.AddCommand(
		buildSessionLsCmd(),
		buildSessionCreateCmd(),
		buildSessionScreenCmd(),
		buildSessionWaitCmd(),
		buildSessionTypeCmd(),
		buildSessionPressCmd(),
		buildSessionDestroyCmd(),
		buildSessionUsageCmd(),
	)
	return cmd
}

// ---------------------------------------------------------------------------
// endpoint + client
// ---------------------------------------------------------------------------

// sessionEndpoint resolves the base URL and access token of a running
// server. The config file is the same one serve reads, so the defaults land
// on the stock setup; precedence is flags > env > config file > token file.
func sessionEndpoint(cmd *cobra.Command) (string, string, error) {
	opts := &api.Options{}
	// The struct tags are the single source of truth for the defaults, so a
	// config file that omits a key behaves exactly like serve.
	if err := config.ApplyDefaultValues(opts); err != nil {
		return "", "", err
	}
	configPath, _ := cmd.Flags().GetString("config")
	if data, err := os.ReadFile(expandHome(configPath)); err == nil && len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, opts); err != nil {
			return "", "", fmt.Errorf("invalid config file %s: %w", configPath, err)
		}
	}

	base, _ := cmd.Flags().GetString("server")
	if base == "" {
		base = os.Getenv("GOSSH_SERVER")
	}
	if base == "" {
		if opts.Port == "0" {
			return "", "", errors.New("the running server chose a random port (--port 0); pass --server http://host:port")
		}
		h := opts.Address
		if h == "" || h == "0.0.0.0" || h == "::" {
			h = "127.0.0.1"
		}
		scheme := "http"
		if opts.EnableTLS {
			scheme = "https"
		}
		base = fmt.Sprintf("%s://%s:%s", scheme, h, opts.Port)
	}
	if !strings.Contains(base, "://") {
		base = "http://" + base
	}
	base = strings.TrimRight(base, "/")

	token, _ := cmd.Flags().GetString("token")
	if token == "" {
		token = os.Getenv("GOSSH_TOKEN")
	}
	if token == "" {
		token = opts.Token
	}
	if token == "" {
		tokenFile, _ := cmd.Flags().GetString("token-file")
		if tokenFile == "" {
			tokenFile = opts.TokenFile
		}
		if tokenFile != "" {
			if data, err := os.ReadFile(expandHome(tokenFile)); err == nil {
				token = strings.TrimSpace(string(data))
			}
		}
	}
	if token == "" {
		return "", "", errors.New("no access token found: pass --token, set GOSSH_TOKEN, or make sure the server's token file is readable")
	}
	return base, token, nil
}

// expandHome expands a leading "~/" through the real home directory.
// utils.Expand only reads $HOME, which is unset in a plain Windows shell;
// os.UserHomeDir() covers that and agrees with $HOME everywhere else.
func expandHome(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	return filepath.Join(home, filepath.FromSlash(path[2:]))
}

// sessionClient is a thin HTTP client for one running server.
type sessionClient struct {
	base  string
	token string
	http  *http.Client
}

func newSessionClient(cmd *cobra.Command) (*sessionClient, error) {
	base, token, err := sessionEndpoint(cmd)
	if err != nil {
		return nil, err
	}
	return &sessionClient{base: base, token: token, http: &http.Client{}}, nil
}

// call issues one request with a plain 30s budget. `wait` does not use this:
// it long-polls, so it passes its own deadline to do().
func (c *sessionClient) call(method, path string, reqBody, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return c.do(ctx, method, path, reqBody, out)
}

func (c *sessionClient) do(ctx context.Context, method, path string, reqBody, out any) error {
	var body io.Reader
	if reqBody != nil {
		payload, err := json.Marshal(reqBody)
		if err != nil {
			return err
		}
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach %s: %w", c.base, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return apiError(resp)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// getBytes fetches a response verbatim (the screen's text or PNG body).
func (c *sessionClient) getBytes(path string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach %s: %w", c.base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp)
	}
	return io.ReadAll(resp.Body)
}

// apiError turns the API's {"error": "..."} body into a Go error, with a hint
// for the two refusals a first run is most likely to hit.
func apiError(resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var payload struct {
		Error string `json:"error"`
	}
	message := strings.TrimSpace(string(raw))
	if json.Unmarshal(raw, &payload) == nil && payload.Error != "" {
		message = payload.Error
	}
	if message == "" {
		message = resp.Status
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("%s (check --token / GOSSH_TOKEN / the server's token file)", message)
	case http.StatusForbidden:
		return fmt.Errorf("%s (the server was started with --permit-write=false)", message)
	case http.StatusServiceUnavailable:
		if strings.Contains(message, "mirror") {
			return fmt.Errorf("%s (the screen mirror is on by default; check --mirror)", message)
		}
	}
	return fmt.Errorf("%s: %s", http.StatusText(resp.StatusCode), message)
}

// ---------------------------------------------------------------------------
// session discovery
// ---------------------------------------------------------------------------

func (c *sessionClient) listSessions() ([]session.StateDescription, error) {
	var payload struct {
		Sessions []session.StateDescription `json:"sessions"`
	}
	if err := c.call(http.MethodGet, "/api/sessions", nil, &payload); err != nil {
		return nil, err
	}
	if payload.Sessions == nil {
		payload.Sessions = []session.StateDescription{}
	}
	return payload.Sessions, nil
}

// resolveSession picks the session a command acts on: the --session value
// (exact id, then unique id prefix, then title or host name), or — when the
// flag is omitted — the one and only session, mirroring tu's "default
// session" convenience.
func (c *sessionClient) resolveSession(cmd *cobra.Command) (session.StateDescription, error) {
	query, _ := cmd.Flags().GetString("session")
	list, err := c.listSessions()
	if err != nil {
		return session.StateDescription{}, err
	}
	if len(list) == 0 {
		return session.StateDescription{}, fmt.Errorf("no sessions on %s (create one with `gossh session create --host <host>`)", c.base)
	}
	if query == "" {
		if len(list) == 1 {
			return list[0], nil
		}
		return session.StateDescription{}, fmt.Errorf("%d sessions are alive, pick one with --session/-s:\n%s", len(list), formatSessions(list))
	}

	for _, s := range list {
		if s.ID == query {
			return s, nil
		}
	}
	var byPrefix []session.StateDescription
	for _, s := range list {
		if strings.HasPrefix(s.ID, query) {
			byPrefix = append(byPrefix, s)
		}
	}
	switch len(byPrefix) {
	case 1:
		return byPrefix[0], nil
	case 0:
		// fall through to title / host-name matching
	default:
		return session.StateDescription{}, fmt.Errorf("session id prefix %q is ambiguous (%d matches):\n%s", query, len(byPrefix), formatSessions(byPrefix))
	}

	var byName []session.StateDescription
	for _, s := range list {
		if (s.Title != "" && s.Title == query) || s.Spec.Name == query {
			byName = append(byName, s)
		}
	}
	switch len(byName) {
	case 1:
		return byName[0], nil
	case 0:
		return session.StateDescription{}, fmt.Errorf("no session matches %q on %s:\n%s", query, c.base, formatSessions(list))
	default:
		return session.StateDescription{}, fmt.Errorf("%q matches %d sessions, use an id instead:\n%s", query, len(byName), formatSessions(byName))
	}
}

// resolveHost turns a host id or name into the id the API wants, asking the
// server (GET /api/hosts) so the CLI works against a remote server too.
func (c *sessionClient) resolveHost(query string) (string, error) {
	var hosts []host.Host
	if err := c.call(http.MethodGet, "/api/hosts", nil, &hosts); err != nil {
		return "", err
	}
	for _, h := range hosts {
		if h.ID == query {
			return h.ID, nil
		}
	}
	var byName []host.Host
	for _, h := range hosts {
		if h.Name == query {
			byName = append(byName, h)
		}
	}
	switch len(byName) {
	case 1:
		return byName[0].ID, nil
	case 0:
		return "", fmt.Errorf("no host matches %q (see `gossh hosts list`)", query)
	default:
		return "", fmt.Errorf("%d hosts are named %q, use the host id", len(byName), query)
	}
}

// ---------------------------------------------------------------------------
// output helpers
// ---------------------------------------------------------------------------

// cliJSON reports whether a command should emit machine-readable JSON:
// --json forces it either way, otherwise a TTY gets the human form and a
// pipe or file gets JSON (terminal-use's convention, so agents piping the
// CLI get structured output for free).
func cliJSON(cmd *cobra.Command) bool {
	if cmd.Flags().Changed("json") {
		forced, _ := cmd.Flags().GetBool("json")
		return forced
	}
	return !stdoutIsTTY()
}

func stdoutIsTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func describeSession(s session.StateDescription) string {
	parts := []string{s.Spec.Name}
	if s.Spec.Addr != "" {
		parts = append(parts, s.Spec.User+"@"+s.Spec.Addr)
	}
	if s.Exited {
		parts = append(parts, "exited")
	}
	return strings.Join(parts, " ")
}

func formatSessions(list []session.StateDescription) string {
	rows := make([][]string, 0, len(list)+1)
	rows = append(rows, []string{"ID", "STATE", "HOST", "ADDRESS", "USER", "TITLE"})
	for _, s := range list {
		state := s.State
		if s.Exited {
			state += " (exited)"
		}
		rows = append(rows, []string{s.ID, state, s.Spec.Name, s.Spec.Addr, s.Spec.User, s.Title})
	}
	return renderTable(rows)
}

// renderTable pads every column but the last, so trailing whitespace never
// ends up in piped output.
func renderTable(rows [][]string) string {
	var widths []int
	for _, row := range rows {
		for i, cell := range row {
			if i >= len(widths) {
				widths = append(widths, 0)
			}
			if n := len([]rune(cell)); n > widths[i] {
				widths[i] = n
			}
		}
	}
	var b strings.Builder
	for _, row := range rows {
		for i, cell := range row {
			b.WriteString(cell)
			if i == len(row)-1 {
				break
			}
			b.WriteString(strings.Repeat(" ", widths[i]-len([]rune(cell))+2))
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// sendBytes writes bytes into a session's PTY (POST /keys). The server still
// decides: the mirror must be on and --permit-write must not be off.
func sendBytes(cmd *cobra.Command, payload []byte, encoding string) error {
	c, err := newSessionClient(cmd)
	if err != nil {
		return err
	}
	sess, err := c.resolveSession(cmd)
	if err != nil {
		return err
	}
	req := map[string]string{"input": string(payload), "encoding": encoding}
	if encoding == "base64" {
		req["input"] = base64.StdEncoding.EncodeToString(payload)
	}
	var res struct {
		Written int `json:"written"`
	}
	if err := c.call(http.MethodPost, "/api/sessions/"+sess.ID+"/keys", req, &res); err != nil {
		return err
	}
	if cliJSON(cmd) {
		return printJSON(struct {
			SessionID string `json:"session_id"`
			Written   int    `json:"written"`
		}{SessionID: sess.ID, Written: res.Written})
	}
	return nil
}

// ---------------------------------------------------------------------------
// subcommands
// ---------------------------------------------------------------------------

func buildSessionLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List the sessions on the running server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newSessionClient(cmd)
			if err != nil {
				return err
			}
			list, err := c.listSessions()
			if err != nil {
				return err
			}
			if cliJSON(cmd) {
				return printJSON(struct {
					Base     string                     `json:"base"`
					Sessions []session.StateDescription `json:"sessions"`
				}{Base: c.base, Sessions: list})
			}
			if len(list) == 0 {
				fmt.Fprintf(os.Stdout, "(no sessions on %s)\n", c.base)
				return nil
			}
			fmt.Fprintln(os.Stdout, formatSessions(list))
			return nil
		},
	}
}

func buildSessionCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create --host <id|name>",
		Short: "Create a session on the running server",
		Long: "Create a session for an inventory host and print its id.\n\n" +
			"With --id the call is idempotent (an alive id is returned as-is) and\n" +
			"resurrects a destroyed one from its recorded spec.\n\n" +
			"Note that --password / --passphrase put a secret in the process list;\n" +
			"prefer key or agent credentials, or a saved keyring entry.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			hostQuery, _ := cmd.Flags().GetString("host")
			if hostQuery == "" {
				return errors.New("--host is required (an inventory host id or name; use `local` for the local server)")
			}
			c, err := newSessionClient(cmd)
			if err != nil {
				return err
			}
			hostID, err := c.resolveHost(hostQuery)
			if err != nil {
				return err
			}

			req := map[string]any{"host_id": hostID}
			if id, _ := cmd.Flags().GetString("id"); id != "" {
				req["id"] = id
			}
			save, _ := cmd.Flags().GetBool("save")
			if password, _ := cmd.Flags().GetString("password"); password != "" {
				req["password"] = password
				if save {
					req["save_password"] = true
				}
			}
			if passphrase, _ := cmd.Flags().GetString("passphrase"); passphrase != "" {
				req["passphrase"] = passphrase
				if save {
					req["save_passphrase"] = true
				}
			}

			var created session.StateDescription
			if err := c.call(http.MethodPost, "/api/sessions", req, &created); err != nil {
				return err
			}
			if cliJSON(cmd) {
				return printJSON(created)
			}
			fmt.Fprintf(os.Stdout, "%s  %s\n", created.ID, describeSession(created))
			return nil
		},
	}
	// No backticks here: pflag treats a backtick-quoted word as the flag's
	// metavar, which would render as `--host local` in --help.
	cmd.Flags().String("host", "", "Inventory host id or name to connect to (the local server's id is \"local\")")
	cmd.Flags().String("id", "", "Client-chosen 16-character base36 session id (default: server-generated)")
	cmd.Flags().String("password", "", "SSH password for this connection only, never persisted")
	cmd.Flags().String("passphrase", "", "Private-key passphrase for this connection only, never persisted")
	cmd.Flags().Bool("save", false, "Move --password / --passphrase into the system keyring after connecting")
	return cmd
}

func buildSessionScreenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "screen",
		Short: "Read a session's rendered screen (text, JSON cells, or PNG)",
		Long: "Read what the session's terminal currently shows.\n\n" +
			"Text (the default) is the plain grid, JSON adds styled cells and\n" +
			"cursor position, --png renders a bitmap. With --png the image goes to\n" +
			"a temp file whose path is printed, to --output, or to stdout.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Validate before touching the network.
			asPNG, _ := cmd.Flags().GetBool("png")
			asJSON := resolveScreenJSON(cmd, asPNG)
			if asPNG && asJSON {
				return errors.New("--png and --json cannot be combined")
			}

			c, err := newSessionClient(cmd)
			if err != nil {
				return err
			}
			sess, err := c.resolveSession(cmd)
			if err != nil {
				return err
			}
			format := "text"
			switch {
			case asPNG:
				format = "png"
			case asJSON:
				format = "json"
			}
			data, err := c.getBytes(fmt.Sprintf("/api/sessions/%s/screen?format=%s", sess.ID, format))
			if err != nil {
				return err
			}
			if asPNG {
				return writeScreenPNG(cmd, data)
			}
			// text and json are already the wire forms; forward them as-is so
			// the CLI never reinterprets the server's contract.
			_, err = os.Stdout.Write(ensureTrailingNewline(data))
			return err
		},
	}
	cmd.Flags().Bool("png", false, "Render a PNG bitmap instead of text")
	cmd.Flags().StringP("output", "o", "", "Write the PNG to this path (default: a temp file, path printed)")
	cmd.Flags().Bool("stdout", false, "Write the PNG bytes to stdout")
	return cmd
}

// resolveScreenJSON decides the screen payload format. --png is a format of
// its own, so it suppresses the "JSON when piped" default: `screen --png >
// shot.png` must write a bitmap rather than complain about a conflict.
func resolveScreenJSON(cmd *cobra.Command, asPNG bool) bool {
	if cmd.Flags().Changed("json") {
		forced, _ := cmd.Flags().GetBool("json")
		return forced
	}
	if asPNG {
		return false
	}
	return !stdoutIsTTY()
}

func ensureTrailingNewline(data []byte) []byte {
	if len(data) == 0 || data[len(data)-1] == '\n' {
		return data
	}
	return append(data, '\n')
}

// writeScreenPNG mirrors tu's screenshot behaviour: an explicit --output, raw
// bytes on --stdout, otherwise a temp file whose path is printed.
func writeScreenPNG(cmd *cobra.Command, data []byte) error {
	toStdout, _ := cmd.Flags().GetBool("stdout")
	output, _ := cmd.Flags().GetString("output")
	switch {
	case toStdout:
		_, err := os.Stdout.Write(data)
		return err
	case output != "":
		path := expandHome(output)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, path)
		return nil
	default:
		f, err := os.CreateTemp("", "gossh-screen-*.png")
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := f.Write(data); err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, f.Name())
		return nil
	}
}

func buildSessionWaitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wait",
		Short: "Block until the screen matches a regex or goes quiet",
		Long: "Block until the session's screen matches --text, or has been\n" +
			"unchanged for --stable milliseconds, then return.\n\n" +
			"Silent on success (exit 0) so it composes as a synchronisation\n" +
			"primitive; on timeout it prints an error and exits 1. Pair it with\n" +
			"`gossh session screen` when you also want the screen contents.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pattern, _ := cmd.Flags().GetString("text")
			stableMS, _ := cmd.Flags().GetInt("stable")
			timeoutMS, _ := cmd.Flags().GetInt("timeout")
			if pattern == "" && stableMS <= 0 {
				return errors.New("--text or --stable is required")
			}
			if timeoutMS <= 0 || timeoutMS > 300000 {
				return errors.New("--timeout must be between 1 and 300000 ms (the server caps a wait at 5 minutes)")
			}

			c, err := newSessionClient(cmd)
			if err != nil {
				return err
			}
			sess, err := c.resolveSession(cmd)
			if err != nil {
				return err
			}

			req := map[string]any{"timeout_ms": timeoutMS}
			if pattern != "" {
				req["regex"] = pattern
			}
			if stableMS > 0 {
				req["quiet_ms"] = stableMS
			}

			// The wait long-polls, so give the request a deadline past the
			// server's own timeout instead of the default 30s.
			ctx, cancel := context.WithTimeout(context.Background(),
				time.Duration(timeoutMS)*time.Millisecond+15*time.Second)
			defer cancel()

			var result struct {
				TakenAt  string          `json:"taken_at"`
				Cols     int             `json:"cols"`
				Rows     int             `json:"rows"`
				Text     string          `json:"text"`
				Cells    json.RawMessage `json:"cells,omitempty"`
				Images   json.RawMessage `json:"images,omitempty"`
				Matched  bool            `json:"matched"`
				Quiet    bool            `json:"quiet"`
				TimedOut bool            `json:"timed_out"`
			}
			if err := c.do(ctx, http.MethodPost, "/api/sessions/"+sess.ID+"/wait", req, &result); err != nil {
				return err
			}
			if cliJSON(cmd) {
				if err := printJSON(result); err != nil {
					return err
				}
			}
			if result.TimedOut {
				return fmt.Errorf("wait timed out after %d ms", timeoutMS)
			}
			return nil
		},
	}
	cmd.Flags().String("text", "", "Wait until this regular expression appears on the screen")
	cmd.Flags().Int("stable", 0, "Wait until the screen has been unchanged for this many milliseconds")
	cmd.Flags().Int("timeout", 30000, "Give up after this many milliseconds (max 300000)")
	return cmd
}

func buildSessionTypeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "type <text>",
		Short: "Type literal text into a session",
		Long: "Type literal text at the session's cursor. Pass --enter to submit a\n" +
			"line, or use `press` for named keys. The server's --permit-write must\n" +
			"be on (it is by default).",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			text := strings.Join(args, " ")
			if enter, _ := cmd.Flags().GetBool("enter"); enter {
				text += "\r"
			}
			return sendBytes(cmd, []byte(text), "text")
		},
	}
	cmd.Flags().Bool("enter", false, "Append Enter after the text")
	return cmd
}

func buildSessionPressCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "press <key>...",
		Short: "Send named keys to a session (Enter, Tab, Ctrl+C, F5, Up, ...)",
		Long: "Send one or more named keys as a single sequence:\n\n" +
			"  gossh session press Enter\n" +
			"  gossh session press Down Down Enter\n" +
			"  gossh session press Escape : w q Enter\n\n" +
			"Names cover Enter/Tab/Escape/Space/Backspace/Delete/Insert, the arrow\n" +
			"and paging keys, F1-F12, modifiers (Ctrl+C, Alt+f, Shift+Tab) and any\n" +
			"single character.\n\n" +
			"The bytes are sent base64-encoded, so sequences stay exact even when\n" +
			"they are not valid UTF-8.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			payload, err := resolveKeys(args)
			if err != nil {
				return err
			}
			return sendBytes(cmd, payload, "base64")
		},
	}
}

func buildSessionDestroyCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "destroy",
		Aliases: []string{"kill"},
		Short:   "Destroy a session (its record stays as history)",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newSessionClient(cmd)
			if err != nil {
				return err
			}
			sess, err := c.resolveSession(cmd)
			if err != nil {
				return err
			}
			if err := c.call(http.MethodDelete, "/api/sessions/"+sess.ID, nil, nil); err != nil {
				return err
			}
			if cliJSON(cmd) {
				return printJSON(struct {
					ID        string `json:"id"`
					Destroyed bool   `json:"destroyed"`
				}{ID: sess.ID, Destroyed: true})
			}
			fmt.Fprintf(os.Stdout, "Destroyed session %s\n", sess.ID)
			return nil
		},
	}
}

func buildSessionUsageCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "usage",
		Short: "Print a compact reference for the session commands",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			fmt.Fprint(os.Stdout, sessionUsageText)
			return nil
		},
	}
}

// sessionUsageText is deliberately terse and LLM-friendly — the audience is an
// agent that was told "gossh session" exists and needs the whole surface at
// once, which is why it exists alongside cobra's --help.
const sessionUsageText = `gossh session -- drive the sessions of a running gossh server from the CLI

A server must already be running (gossh serve / gossh app). This CLI is
stateless: every command makes one or two HTTP calls and exits.

COMMANDS:
  ls                                  List the sessions (id, state, host, title)
  create --host <id|name>             Create a session, print its id
    --id <16 base36 chars>              Client-chosen id (idempotent / resurrects)
    --password <pw>                     SSH password for this connect only
    --passphrase <pp>                   Key passphrase for this connect only
    --save                              Move the secret into the system keyring
  screen                              Read the rendered screen
    --png                               Render a PNG instead of text
    -o/--output <file>                  PNG path (default: temp file, path printed)
    --stdout                            PNG bytes to stdout
  wait --text <regex>                 Block until the screen matches
    --stable <ms>                       …or until the screen is unchanged this long
    --timeout <ms>                      Give up after N ms (default 30000, max 300000)
  type <text>                         Type literal text
    --enter                             Append Enter
  press <key>...                      Send named keys, space separated
  destroy | kill                      Destroy the session (record kept as history)
  usage                               This text

GLOBAL FLAGS:
  -s/--session <id|prefix|title>      Target session; default: the only one alive
  --json                              Emit JSON (default: JSON when piped, text on a TTY)
  --server <url>                      Default: from the config file (~/.gossh/config.json)
  --token <token>                     Default: GOSSH_TOKEN, then the token file

KEYS:  Enter Tab Escape Space Backspace Delete Insert
       Up Down Left Right Home End PageUp PageDown
       F1-F12
       Ctrl+C   Alt+f   Shift+Tab   Ctrl+Shift+Up
       any single character

EXAMPLES:
  gossh session ls
  gossh session create --host prod
  gossh session type "ls -la" --enter
  gossh session wait --text 'password:' --timeout 20000
  gossh session screen
  gossh session screen --png -o shot.png
  gossh session press Ctrl+C
  gossh session press Escape : w q Enter
  gossh session destroy -s prod
`
