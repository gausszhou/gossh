# 桌面形态:浏览器 UI + 托盘封装,而非独立桌面窗口

`gossh app` 提供 Linux 桌面集成(托盘常驻、开机自启、单实例、自动开浏览器并注入令牌),UI 仍是嵌入式网页。

## 决策

桌面形态 = 在单二进制内新增 `gossh app` 子命令,复用 `serve` 的服务端装配与嵌入了的 Web UI,补一层桌面外壳:

- **托盘**(getlantern/systray,仅 `linux && cgo` 构建启用):菜单「打开界面 / 开机自启 / 退出」;无 cgo/无托盘的构建与平台明确报错而不是静默降级。
- **常驻语义**:服务与托盘同进程,浏览器关闭不影响会话(沿用 idle-timeout);托盘「退出」才停服。
- **单实例**:`flock ~/.gossh/app.lock`;第二实例只「打开已有实例的界面」并退出。
- **安全**:令牌门禁一字不改,自动开浏览器时注入 `?token=`;不引入任何旁路。
- **开机自启**:状态 = `~/.config/autostart/gossh.desktop` 文件存在性;Exec 优先 `$APPIMAGE`(AppImage 运行时指向真实文件)回退 `os.Executable()`;自启用 `--no-browser` 静默进托盘。
- **分发**:release CI 对 Linux amd64/arm64 各产一个 AppImage(`packaging/linux/` 骨架 + `assets/icon.png`);install.sh 不动。

## 理由

- 产品身份是「浏览器为 UI 的 SSH 客户端」(ADR-0002);用户的别扭点是打开方式与系统集成(无图标/托盘/自启、复制令牌),不是"要一个原生窗口"。
- 托盘封装**零新运行时**:服务端、前端、密钥/清单/会话存储全部原样复用,不引入二级进程、不改变数据流。
- **Tauri/Electron 被明确拒绝的核心理由**:
  - Electron:自带 Chromium ~100MB+,与「五平台单二进制 + tar.gz 几千字节增量」的轻量分发定位直接冲突,且引入 Node/Chromium 升级泥潭。
  - Tauri:体积虽小,但 Linux 依赖系统 WebKitGTK,不同发行版表现飘忽;还要引入 Rust 工具链进构建矩阵。
  - 两者都要**重写前端外壳**(窗口/菜单/托盘实现语言切换),而 GoSSH 前端是纯 Vite 静态页,毫无收益。
- 纯 Go 零依赖托盘(自研 D-Bus StatusNotifierItem)当时评估过:依赖最干净,但 SNI 协议在 GNOME/KDE 的兼容坑要自己长期维护,收益率远低于成熟库。

## 代价

- Linux 的 `gossh app` 需要 cgo 构建(`CGO_ENABLED=1` + GTK/AppIndicator 头);默认 `make build/release`(`CGO_ENABLED=0`)产出的二进制 `gossh app` 会报"需要 cgo"——桌面形态只在 AppImage(CI cgo 构建)与本机 cgo 构建中可用。
- 托盘依赖桌面环境的 AppIndicator/SNI 支持(GNOME 需装扩展,Ubuntu 预装);纯 headless 环境无托盘,`gossh app` 明确报错,用 `gossh serve`。
- arm64 AppImage 需要 CI 交叉工具链(GTKK arm64 头 + 交叉 gcc),是发布流水线上最易碎的一环。

## 备选

- 浏览器 + 零集成(现状):`gossh serve` 保留,命令行用户完全不受影响。
- 独立桌面窗口(Tauri/Electron):被拒,理由如上。
- 系统服务化(systemd 常驻):与托盘方案互补而非替代;托盘已是常驻入口,暂不引入 systemd unit。