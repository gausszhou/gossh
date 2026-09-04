//go:build !linux

package desktop

// IsAutostart 非 linux 平台暂不支持。
func IsAutostart() bool { return false }

// SetAutostart 非 linux 平台暂不支持。
func SetAutostart(enabled bool) error { return errUnsupported }
