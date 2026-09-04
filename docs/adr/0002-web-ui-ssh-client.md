# 用 Web UI 承载 SSH 会话管理,而非 TUI / 纯 CLI

GoSSH 是一个 SSH 客户端,但其客户端 UI 是浏览器(Vue3 + xterm.js),而不是终端内全屏 TUI 或纯 `ssh` 式 CLI。本机跑一个 Go 服务端(listen 127.0.0.1),浏览器即客户端。

理由:
- **多会话页签、列表、弹窗、文件浏览的 UI 成本**在浏览器里远比 TUI 低。xterm.js 提供与真实终端等价的渲染(WebGL、CJK、图形协议),Vue 组件化覆盖主机清单 CRUD、凭据输入、SFTP 浏览、转发面板等 TUI 需要手写的密集交互。
- **单二进制可交付**:前端经 `go:embed` 内嵌,产物仍是单个可执行文件(`gossh-{os}-{arch}`),无 Node 运行时依赖。
- **AI/脚本可驱动**:HTTP REST + WebSocket 协议让自动化(agent driving、curl、测试)与人工使用同构。

代价:额外一个本地 HTTP 服务进程与安全入口(访问令牌),见 ADR 0005;浏览器对终端 I/O 的转发经由自定义二进制帧协议(继承自 gotty,见 ADR 0003)。

备选:(a) TUI 会话管理器——终端语义最自然,但主机管理/表单/多视图的开发量大且可测试性差,已否决;(b) 纯 CLI 包装 OpenSSH——无差异化,已否决。