//go:build !linux

package desktop

// RunTray 非 linux 平台暂不支持(Windows/macOS 为 P2)。
func RunTray(o TrayOptions) error { return errUnsupported }
