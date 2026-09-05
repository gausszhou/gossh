//go:build !run

package cmd

import "github.com/spf13/cobra"

// registerRunCmd 是 `gossh run` 禁用态的桩:默认构建(Makefile RUN=0)下
// 单命令执行子命令不注册,`POST /api/run` 也不存在。
func registerRunCmd(root *cobra.Command) {}
