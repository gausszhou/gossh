//go:build !linux && !windows

package desktop

// RunTray 未支持平台暂不可用(macOS 等为 P2)。
func RunTray(o TrayOptions) error { return errUnsupported }
