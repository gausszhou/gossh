//go:build run

package cmd

import "github.com/spf13/cobra"

// registerRunCmd 注册 `gossh run` 子命令(仅 -tags run 构建;默认构建
// 不提供单命令执行,见 run_cmd_disabled.go)。
func registerRunCmd(root *cobra.Command) {
	root.AddCommand(buildRunCmd())
}
