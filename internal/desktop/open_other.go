//go:build !linux

package desktop

// OpenBrowser 非 linux 平台暂不支持。
func OpenBrowser(url string) error { return errUnsupported }
