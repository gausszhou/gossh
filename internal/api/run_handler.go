package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gausszhou/gossh/internal/sshx"

	"golang.org/x/crypto/ssh"
)

// runRequest is the body of POST /api/run.
type runRequest struct {
	HostID         string   `json:"host_id"`
	Command        string   `json:"command"`        // e.g. "uptime" or "sh -c '...'"; empty → login shell prompt rejected
	Args           []string `json:"args,omitempty"` // appended as arguments (no shell quoting needed)
	Password       *string  `json:"password,omitempty"`
	Passphrase     *string  `json:"passphrase,omitempty"`
	SavePassword   bool     `json:"save_password,omitempty"`
	SavePassphrase bool     `json:"save_passphrase,omitempty"`
	TimeoutMs      int      `json:"timeout_ms,omitempty"` // 0 = default 60s; -1 = no timeout
}

// runResponse is the result of one single-command execution.
type runResponse struct {
	HostID    string `json:"host_id"`
	Name      string `json:"name"`
	Command   string `json:"command"`
	Output    string `json:"output"`
	ExitCode  int    `json:"exit_code"` // -1 = did not exit normally (timeout/network)
	Error     string `json:"error,omitempty"`
	Duration  int64  `json:"duration_ms"`
	HostKeyOk bool   `json:"host_key_ok"` // false → host key was pinned this run (TOFU)
}

// handleRun implements POST /api/run — non-interactive single command
// execution on a host (the browser-side twin of `gossh run`).
func (server *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	var req runRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.HostID == "" || strings.TrimSpace(req.Command) == "" {
		writeError(w, http.StatusBadRequest, "host_id and command are required")
		return
	}

	timeout := 60 * time.Second
	switch {
	case req.TimeoutMs < 0:
		timeout = 0
	case req.TimeoutMs > 0:
		timeout = time.Duration(req.TimeoutMs) * time.Millisecond
	}

	start := time.Now()
	resp, err := server.executeRun(req, timeout)
	if err != nil {
		log.Printf("Run failed on %s: %s", req.HostID, err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Duration = time.Since(start).Milliseconds()

	if req.SavePassword && req.Password != nil && err == nil {
		_ = server.savePasswordFor(req.HostID, *req.Password)
	}
	if req.SavePassphrase && req.Passphrase != nil && err == nil {
		if h, herr := server.inventory.Get(req.HostID); herr == nil && h.Credential.Kind == "key" {
			_ = server.secrets.SetPassphrase(h.Credential.KeyPath, *req.Passphrase)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// executeRun dials the host chain and runs the command once.
func (server *Server) executeRun(req runRequest, timeout time.Duration) (*runResponse, error) {
	h, err := server.inventory.Get(req.HostID)
	if err != nil {
		return nil, errors.New("host not found")
	}
	chain, err := server.inventory.Chain(req.HostID)
	if err != nil {
		return nil, err
	}

	prov := &sshx.ProvidedSecrets{}
	if req.Password != nil && *req.Password != "" {
		prov.Password = req.Password
	}
	if req.Passphrase != nil && *req.Passphrase != "" {
		prov.Passphrase = req.Passphrase
	}

	hops, err := sshx.BuildHops(chain, server.secrets, prov, server.knownHosts, server.connectTimeout())
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout+server.connectTimeout()*time.Duration(len(hops)))
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	dial, err := sshx.DialChain(ctx, hops)
	if err != nil {
		return nil, err
	}
	defer dial.Close()

	sess, err := dial.Target.NewSession()
	if err != nil {
		return nil, err
	}
	defer sess.Close()

	// Compose the command line: command + args verbatim (no shell unless asked).
	cmd := req.Command
	if len(req.Args) > 0 {
		cmd = cmd + " " + strings.Join(req.Args, " ")
	}

	// Restrict the session: no PTY, no terminal.
	output, runErr := sess.CombinedOutput(cmd)

	exitCode := 0
	if runErr != nil {
		var ee *ssh.ExitError
		if errors.As(runErr, &ee) {
			exitCode = ee.ExitStatus()
		} else {
			exitCode = -1
		}
	}
	errStr := ""
	if runErr != nil && exitCode == -1 {
		errStr = runErr.Error()
	}

	return &runResponse{
		HostID:   req.HostID,
		Name:     h.Name,
		Command:  cmd,
		Output:   string(output),
		ExitCode: exitCode,
		Error:    errStr,
	}, nil
}

// savePasswordFor stores the password for the host's user@addr in the
// keyring (the browser "save to keyring" checkbox).
func (server *Server) savePasswordFor(hostID, password string) error {
	h, err := server.inventory.Get(hostID)
	if err != nil {
		return err
	}
	return server.secrets.SetPassword(h.Addr(), h.User, password)
}
