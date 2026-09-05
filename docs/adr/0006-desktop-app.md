# 桌面形态:浏览器 UI + 托盘封装,而非独立桌面窗口

`gossh app` 提供 Linux 与 Windows 桌面集成(托盘常驻、开机自启、单实例、自动开浏览器并注入令牌),UI 仍是嵌入式网页。

## 决策

桌面形态 = 在单二进制内新增 `gossh app` 子命令,复用 `serve` 的服务端装配与嵌入了的 Web UI,补一层桌面外壳:

- **托盘**(getlantern/systray):
  - Linux:仅 `linux && cgo` 构建启用(GTK/AppIndicator);无 cgo/无托盘的构建与平台明确报错而不是静默降级。
  - Windows:纯 Go 实现(win32 消息循环,`systray_windows.go` 无 cgo),**默认 `CGO_ENABLED=0` 的 release 矩阵(native windows/amd64 二进制)即可用 `gossh app`**,无需 AppImage 等附加分发形态。
- **常驻语义**:服务与托盘同进程,浏览器关闭不影响会话(沿用 idle-timeout);托盘「退出」才停服。
- **单实例**:Linux 用 `flock ~/.gossh/app.lock`;Windows 用会话命名空间命名互斥体 `Local\gossh-app-<用户>`(无 flock;无需 SeCreateGlobalPrivilege)。第二实例只「打开已有实例的界面」并退出。两者均在进程崩溃时自动释放,不残留死锁。
- **安全**:令牌门禁一字不改,自动开浏览器时注入 `?token=`;不引入任何旁路。
- **开机自启**:
  - Linux:状态 = `~/.config/autostart/gossh.desktop` 文件存在性;Exec 优先 `$APPIMAGE`(AppImage 运行时指向真实文件)回退 `os.Executable()`;自启用 `--no-browser` 静默进托盘。
  - Windows:状态 = HKCU `Software\Microsoft\Windows\CurrentVersion\Run` 下 `GoSSH` 值的存在性;值 = 引号包裹的 `os.Executable()` + ` app --no-browser`。用户级自启,无需管理员。
- **打开浏览器**:Linux 用 `xdg-open`;Windows 用 `rundll32 url.dll,FileProtocolHandler`(隐藏窗口,不等待)。
- **分发**:release CI 对 Linux 产 AppImage(`packaging/linux/` 骨架 + `assets/icon.png`);Windows 走既有 `windows/amd64` 二进制矩阵,无额外产物。v0.1.0 起 AppImage 仅发布 amd64:arm64 交叉工具链(runner 默认源不提供 arm64 索引,注册 arm64 架构后 `apt-get update` 404 退出 100)连续两轮 CI 失败后暂缓,详见 release.yml 注释与本节代价。

## 理由

- 产品身份是「浏览器为 UI 的 SSH 客户端」(ADR-0002);用户的别扭点是打开方式与系统集成(无图标/托盘/自启、复制令牌),不是"要一个原生窗口"。
- 托盘封装**零新运行时**:服务端、前端、密钥/清单/会话存储全部原样复用,不引入二级进程、不改变数据流。
- **Tauri/Electron 被明确拒绝的核心理由**:
  - Electron:自带 Chromium ~100MB+,与「五平台单二进制 + tar.gz 几千字节增量」的轻量分发定位直接冲突,且引入 Node/Chromium 升级泥潭。
  - Tauri:体积虽小,但 Linux 依赖系统 WebKitGTK,不同发行版表现飘忽;还要引入 Rust 工具链进构建矩阵。
  - 两者都要**重写前端外壳**(窗口/菜单/托盘实现语言切换),而 GoSSH 前端是纯 Vite 静态页,毫无收益。
- 纯 Go 零依赖托盘(自研 D-Bus StatusNotifierItem)当时评估过:依赖最干净,但 SNI 协议在 GNOME/KDE 的兼容坑要自己长期维护,收益率远低于成熟库。

## 代价

- Linux 的 `gossh app` 需要 cgo 构建(`CGO_ENABLED=1` + GTK/AppIndicator 头);默认 `make build/release`(`CGO_ENABLED=0`)产出的二进制 `gossh app` 会报"需要 cgo"——Linux 桌面形态只在 AppImage(CI cgo 构建)与本机 cgo 构建中可用。
- Linux 托盘依赖桌面环境的 AppIndicator/SNI 支持(GNOME 需装扩展,Ubuntu 预装);纯 headless 环境无托盘,`gossh app` 明确报错,用 `gossh serve`。
- Windows 无上述两条代价(托盘纯 Go、release 二进制即用);仅有的取舍是单实例互斥体按会话命名空间隔离(不同 RDP 会话不互斥,与 Linux 每用户一把 flock 的语义略有差异,托盘本就按会话呈现,可接受)。
- arm64 AppImage 需要 CI 交叉工具链(目标架构 GTK 头 + 交叉 gcc + 交叉 pkg-config),是发布流水线上最易碎的一环:v0.1.0 尝试两轮失败(runner 默认源不提供 arm64 索引),已暂缓为仅发布 amd64 AppImage;恢复时需为 arm64 另配 ports.ubuntu.com 源段并解决 `security.ubuntu.com` 索引缺失。

## 备选

- 浏览器 + 零集成(现状):`gossh serve` 保留,命令行用户完全不受影响。
- 独立桌面窗口(Tauri/Electron):被拒,理由如上。
- 系统服务化(systemd 常驻):与托盘方案互补而非替代;托盘已是常驻入口,暂不引入 systemd unit。