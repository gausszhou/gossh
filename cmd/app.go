package cmd

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/spf13/cobra"

	"github.com/gausszhou/gossh/internal/api"
	"github.com/gausszhou/gossh/internal/config"
	"github.com/gausszhou/gossh/internal/desktop"
	"github.com/gausszhou/gossh/internal/utils"
)

// appMappings 是 `gossh app` 自己的 flag→默认值映射(与 serve 的命令/flag
// 集合不同,不能共用以包级变量的那套 mappings)。
var appMappings map[string]string

// buildAppCmd 创建 `gossh app` 子命令(桌面形态)。
//
// 服务端能力与 serve 完全一致(同一套 Options 装配,flags 全继承),额外
// 桌面行为:单实例锁 → 自动开浏览器(token 注入 URL)→ 托盘常驻 →
// 开机自启开关。--no-browser 供开机自启条目使用(静默进托盘)。
func buildAppCmd() *cobra.Command {
	appCmd := &cobra.Command{
		Use:   "app [flags]",
		Short: "Run GoSSH as a desktop app on Linux (tray + browser UI)",
		Long: "Run GoSSH as a desktop app on Linux (tray + browser UI).\n\n" +
			"The server runs in the same process and stays resident in the\n" +
			"system tray: closing the browser does not stop sessions; only\n" +
			"the tray \"quit\" item stops the server. The browser opens\n" +
			"automatically with the access token injected. A second\n" +
			"invocation just opens the UI of the running instance.",
		Args: cobra.NoArgs,
		RunE: runApp,
	}

	if err := config.ApplyDefaultValues(appOptions); err != nil {
		panic(err)
	}
	if err := config.ApplyDefaultValues(terminalOptions); err != nil {
		panic(err)
	}
	m, err := config.AttachFlags(appCmd, appOptions, terminalOptions)
	if err != nil {
		panic(err)
	}
	appMappings = m

	appCmd.Flags().Bool("no-browser", false, "Do not open the browser automatically (used by the autostart entry)")

	return appCmd
}

func runApp(cmd *cobra.Command, args []string) error {
	noBrowser, _ := cmd.Flags().GetBool("no-browser")

	// 单实例:先拿锁再启动;第二实例只负责「打开已有实例的界面」并退出
	release, held, err := desktop.TryLock(desktop.DesktopLockPath())
	if err != nil {
		return err
	}
	if !held {
		log.Printf("Another GoSSH desktop instance is running.")
		if !noBrowser {
			if u, uerr := uiURLFromOptions(); uerr == nil {
				if oerr := desktop.OpenBrowser(u); oerr != nil {
					log.Printf("desktop: failed to open the browser: %s", oerr)
				}
			} else {
				log.Printf("desktop: cannot determine the running UI URL (%s); use the tray menu of the running instance", uerr)
			}
		}
		return nil
	}
	defer release()

	srv, err := assembleServer(cmd, appMappings)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // 提前返回路径也不泄漏 ctx(托盘退出经 Stop 再调,幂等)
	gCtx, gCancel := context.WithCancel(context.Background())
	defer gCancel()

	errs := make(chan error, 1)
	uiURLCh := make(chan string, 1)
	go func() {
		errs <- srv.Run(ctx, api.WithGracefullContext(gCtx), api.WithURLHook(func(u string) {
			uiURLCh <- u
		}))
	}()

	// 单一读取方:服务错误只在 done 关闭后经 runErr 取用
	done := make(chan struct{})
	var runErr error
	go func() {
		runErr = <-errs
		close(done)
	}()

	// 等服务就绪(拿到真实监听端口的 URL,支持 --port 0)或启动失败
	var uiURL string
	select {
	case uiURL = <-uiURLCh:
	case <-done:
		return runErr
	}

	if !noBrowser {
		if oerr := desktop.OpenBrowser(uiURL); oerr != nil {
			log.Printf("desktop: failed to open the browser: %s", oerr)
		}
	} else {
		log.Printf("desktop: running without opening the browser (--no-browser)")
	}

	// 托盘常驻:服务退出(done)会关闭 Closed,托盘随之退出
	if terr := desktop.RunTray(desktop.TrayOptions{
		UIURL:  uiURL,
		Stop:   cancel,
		Closed: done,
	}); terr != nil {
		return terr
	}

	// 托盘返回 = 用户点了「退出」(cancel 已触发)或服务已先退出
	<-done
	if runErr != nil && runErr != context.Canceled {
		return runErr
	}
	return nil
}

// uiURLFromOptions 第二实例拼 UI URL:端口与令牌全部来自配置(与运行中
// 实例同源)。--port 0(随机端口)或令牌不可解析时无法确定 → 报错,
// 由运行中实例的托盘「打开界面」兜底。
func uiURLFromOptions() (string, error) {
	if appOptions.Port == "0" {
		return "", fmt.Errorf("the running instance uses a random port (--port 0)")
	}
	token := appOptions.Token
	if token == "" {
		// 与 api.ResolveToken 相同的读取规则,但绝不生成新令牌——
		// 第二实例必须复用运行中实例的令牌才能通过门禁
		if data, rerr := os.ReadFile(utils.Expand(appOptions.TokenFile)); rerr == nil && len(data) > 0 {
			token = string(data)
		}
	}
	if token == "" {
		return "", fmt.Errorf("cannot determine the access token (no --token and no readable token file)")
	}
	browserHost := appOptions.Address
	if browserHost == "0.0.0.0" || browserHost == "::" || browserHost == "" {
		browserHost = "127.0.0.1"
	}
	scheme := "http"
	if appOptions.EnableTLS {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s:%s/?token=%s", scheme, browserHost, appOptions.Port, token), nil
}
