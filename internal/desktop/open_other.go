//go:build !linux && !windows

package desktop

// OpenBrowser 未支持平台暂不可用。
func OpenBrowser(url string) error { return errUnsupported }
