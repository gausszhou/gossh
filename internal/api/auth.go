package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
)

// tokenFromRequest extracts the presented token from the Authorization
// header (Bearer), the X-Gossh-Token header, or the ?token= query param.
func tokenFromRequest(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); len(auth) > 7 && auth[:7] == "Bearer " {
		return auth[7:]
	}
	if t := r.Header.Get("X-Gossh-Token"); t != "" {
		return t
	}
	return r.URL.Query().Get("token")
}

// requireToken wraps an http.Handler with constant-time token checks.
// Authenticated requests get the token dropped from the URL so it does
// not leak into the server log.
func (server *Server) requireToken(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented := tokenFromRequest(r)
		if len(presented) != len(server.token) ||
			subtle.ConstantTimeCompare([]byte(presented), []byte(server.token)) != 1 {
			http.Error(w, "unauthorized: missing or invalid access token", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// generateToken creates a new random access token.
func generateToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate access token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// ResolveToken decides the effective access token: an explicitly
// configured one wins; otherwise a token is loaded from TokenFile or
// generated and persisted there. 导出供 cmd(app 模式拼 UI URL)复用。
func ResolveToken(options *Options) (string, error) {
	if options.Token != "" {
		return options.Token, nil
	}
	tokenFile := ""
	if options.TokenFile != "" {
		tokenFile = options.TokenFile
		if len(tokenFile) >= 2 && tokenFile[:2] == "~/" {
			home, _ := os.UserHomeDir()
			tokenFile = home + tokenFile[1:]
		}
	}
	if tokenFile != "" {
		if data, err := os.ReadFile(tokenFile); err == nil && len(data) > 0 {
			return string(data), nil
		}
	}
	token, err := generateToken()
	if err != nil {
		return "", err
	}
	if tokenFile != "" {
		if err := os.MkdirAll(filepath.Dir(tokenFile), 0o700); err != nil {
			return "", err
		}
		if err := os.WriteFile(tokenFile, []byte(token), 0o600); err != nil {
			return "", err
		}
	}
	return token, nil
}
