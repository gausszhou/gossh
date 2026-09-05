//go:build !linux && !windows

package desktop

// IsAutostart 未支持平台暂不可用。
func IsAutostart() bool { return false }

// SetAutostart 未支持平台暂不可用。
func SetAutostart(enabled bool) error { return errUnsupported }
