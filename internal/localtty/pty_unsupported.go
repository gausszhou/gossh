//go:build !linux && !darwin && !windows

package localtty

import "errors"

// startPTY 未支持平台暂不可用:本地服务器需要 PTY(Unix /dev/ptmx、
// Windows ConPTY),其他平台没有实现。发布矩阵只覆盖 linux/darwin/
// windows(Makefile PLATFORMS),这里只是让 `go build ./...` 在别的
// 平台上仍然能编译通过。
func startPTY(cfg ptyConfig) (ptyHandle, error) {
	return nil, errors.New("local shell is not supported on this platform")
}
