package terminal

import (
	"syscall"
	"time"
)

// Default values used when constructing a session.
const (
	DefaultCloseSignal  = syscall.SIGHUP
	DefaultCloseTimeout = 3 * time.Second
)

// Options configures how sessions are closed and which terminal type
// is requested from the remote side.
type Options struct {
	// CloseSignal is sent to the remote process when the session closes.
	CloseSignal int `json:"close_signal" flagName:"close-signal" flagDescribe:"Signal sent to the remote process when the session is closed" default:"1"`

	// CloseTimeout is the grace period between the close signal and a
	// hard kill (SIGKILL) of the remote session. -1 disables escalation.
	CloseTimeout int `json:"close_timeout" flagName:"close-timeout" flagDescribe:"Seconds to wait for the close signal before sending SIGKILL" default:"3"`

	// Term is the TERM value requested for the remote PTY.
	// Empty means "xterm-256color".
	Term string `json:"term"`

	// DialCredentials carries per-connect secrets for the terminal
	// factory (never persisted).
	DialCredentials *DialCredentials `json:"-"`
}

// DialCredentials carries per-connect authentication secrets from the
// create request through the option chain to the terminal factory. They
// are used for exactly one connection attempt and never persisted.
type DialCredentials struct {
	Password   string `json:"-"`
	Passphrase string `json:"-"`
}

// Option is a functional option of session construction.
type Option func(*Options)

// WithCloseSignal sets the signal sent to the remote process on Close.
func WithCloseSignal(signal syscall.Signal) Option {
	return func(o *Options) {
		o.CloseSignal = int(signal)
	}
}

// WithCloseTimeout sets the grace period before SIGKILL on Close.
// A negative duration disables the kill escalation.
func WithCloseTimeout(timeout time.Duration) Option {
	return func(o *Options) {
		o.CloseTimeout = int(timeout / time.Second)
	}
}

// WithTerm sets the TERM value requested for the remote PTY.
func WithTerm(term string) Option {
	return func(o *Options) {
		o.Term = term
	}
}

// WithDialCredentials attaches per-connect secrets for the terminal
// factory (single attempt, never persisted).
func WithDialCredentials(secrets *DialCredentials) Option {
	return func(o *Options) {
		o.DialCredentials = secrets
	}
}

// Apply folds options into a fresh Options value.
func Apply(opts ...Option) Options {
	o := Options{
		CloseSignal:  int(DefaultCloseSignal),
		CloseTimeout: int(DefaultCloseTimeout / time.Second),
		Term:         "xterm-256color",
	}
	for _, opt := range opts {
		opt(&o)
	}
	return o
}
