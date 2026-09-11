package cmd

import (
	"bytes"
	"context"
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
)

// This file holds what every server-facing command group shares: the
// persistent connection flags, the base-URL + token resolution, the thin
// HTTP client, and the output conventions.
//
// The model — borrowed from terminal-use for `gossh session` — is that the
// server owns all state (sessions, hosts, pins, secrets) and the CLI is a
// stateless, one-shot HTTP client. It therefore always talks to a *running*
// `gossh serve` / `gossh app`: editing the files underneath a running server
// would be invisible to it (the inventory is loaded once at startup) and its
// next write would clobber ours.

// addAPIFlags installs the persistent flags every server-facing group
// accepts. The root command contributes --config, which apiEndpoint reads.
func addAPIFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().String("server", "", "Base URL of the running server (default: derived from the config file)")
	cmd.PersistentFlags().String("token", "", "Access token (default: GOSSH_TOKEN, config file, then the token file)")
	cmd.PersistentFlags().String("token-file", "", "File holding the access token (default: the config file's token_file)")
	cmd.PersistentFlags().Bool("json", false, "Emit JSON (default: human-readable on a TTY, JSON when piped)")
}

// apiEndpoint resolves the base URL and access token of a running server.
// The config file is the same one serve reads, so the defaults land on the
// stock setup; precedence is flags > env > config file > token file.
func apiEndpoint(cmd *cobra.Command) (string, string, error) {
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

// apiClient is a thin HTTP client for one running server.
type apiClient struct {
	base  string
	token string
	http  *http.Client
}

func newAPIClient(cmd *cobra.Command) (*apiClient, error) {
	base, token, err := apiEndpoint(cmd)
	if err != nil {
		return nil, err
	}
	return &apiClient{base: base, token: token, http: &http.Client{}}, nil
}

// call issues one request with a plain 30s budget. `session wait` does not
// use this: it long-polls, so it passes its own deadline to do().
func (c *apiClient) call(method, path string, reqBody, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return c.do(ctx, method, path, reqBody, out)
}

func (c *apiClient) do(ctx context.Context, method, path string, reqBody, out any) error {
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
func (c *apiClient) getBytes(path string) ([]byte, error) {
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
// for the refusals a first run is most likely to hit.
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
// host targeting
// ---------------------------------------------------------------------------

// listHosts returns the inventory as the server sees it (the built-in local
// server is first).
func (c *apiClient) listHosts() ([]host.Host, error) {
	var hosts []host.Host
	if err := c.call(http.MethodGet, "/api/hosts", nil, &hosts); err != nil {
		return nil, err
	}
	if hosts == nil {
		hosts = []host.Host{}
	}
	return hosts, nil
}

// resolveHost turns a host id or name into the id the API wants, asking the
// server (GET /api/hosts) so the CLI works against a remote server too.
func (c *apiClient) resolveHost(query string) (string, error) {
	hosts, err := c.listHosts()
	if err != nil {
		return "", err
	}
	for _, h := range hosts {
		if h.ID == query {
			return h.ID, nil
		}
	}
	var byName []host.Host
	for _, h := range hosts {
		if strings.EqualFold(h.Name, query) {
			byName = append(byName, h)
		}
	}
	switch len(byName) {
	case 1:
		return byName[0].ID, nil
	case 0:
		return "", fmt.Errorf("no host matches %q (see `gossh hosts ls`)", query)
	default:
		return "", fmt.Errorf("%d hosts are named %q, use the host id", len(byName), query)
	}
}

// getHost resolves a host id/name and returns the full record.
func (c *apiClient) getHost(query string) (host.Host, error) {
	id, err := c.resolveHost(query)
	if err != nil {
		return host.Host{}, err
	}
	var h host.Host
	if err := c.call(http.MethodGet, "/api/hosts/"+id, nil, &h); err != nil {
		return host.Host{}, err
	}
	return h, nil
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

// requireFlag returns a flag's value or a uniform "missing" error, so every
// command fails the same way before touching the network.
func requireFlag(cmd *cobra.Command, name, hint string) (string, error) {
	value, _ := cmd.Flags().GetString(name)
	if value == "" {
		return "", fmt.Errorf("%s", hint)
	}
	return value, nil
}
