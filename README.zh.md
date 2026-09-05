# GoSSH — 浏览器里的 SSH 客户端

[English](README.md) | **简体中文**

一个基于 Go 技术栈的 SSH 客户端:本地运行一个服务端,浏览器就是你的终端 UI。
管理主机清单、多会话页签、SFTP 文件传输、端口转发、跳板链、凭据入库(keyring)。

```
gossh serve
# HTTP server is listening at: http://127.0.0.1:8040
# Open the page with the access token:
#   http://127.0.0.1:8040/?token=4f0a...
```

打开浏览器地址即可使用。整个服务只监听本机,令牌护体——私钥不出进程。

## 特性

- **主机清单**:CRUD、分组、搜索、`via` 跳板链(任意深度,ProxyJump 语义)
- **多会话页签**:SSH 会话页签可左右拖拽排序,顺序按设备持久化
  (localStorage `gossh.tabOrder`)
- **单命令执行**(编译期可选):`gossh run <host> '<cmd>'`(退出码直通)与
  浏览器运行结果页签。**默认构建不编译**——`make build RUN=1` 启用
  (`-tags run` + 前端 `VITE_RUN=1`)
- **SFTP**(编译期可选):在会话连接上浏览/传输文件。**默认构建不编译**
  以保持二进制精简——`make build SFTP=1` 启用(Go `-tags sftp` + 前端
  `VITE_SFTP=1`,Makefile 已同源接线)
- **凭据**:私钥文件路径引用、ssh-agent、密码;密码与密钥口令经系统 keyring
  (Linux Secret Service / macOS Keychain / Windows Credential Manager)加密保存,
  无 keyring 守护进程时自动回退为内存保存
- **主机密钥**:TOFU 信任管理(`~/.gossh/known_hosts`),指纹变更即拒绝连接
- **端口转发**:local / remote / dynamic(SOCKS5);主机级转发跑在主机专属的
  转发连接上,**不随会话生灭**——关终端页签/销毁会话转发仍在
  (见 [ADR 0007](docs/adr/0007-host-forwards-resident.md))
- **断开存活**:浏览器断开或刷新,SSH 会话继续存活,空闲超时(默认 900s)后销毁
- **单命令执行**:`gossh run <host> '<cmd>'` 无浏览器直跑,浏览器内也有入口
- **单二进制交付**:前端经 `go:embed` 内嵌,跨平台编译无需 Node 运行时
- **桌面形态(Linux)**:`gossh app` 托盘常驻、开机自启、单实例;自动开浏览器并
  注入令牌;Release 附带免安装的 AppImage

## 安装

```sh
curl -fsSL https://raw.githubusercontent.com/gausszhou/gossh/main/scripts/install.sh | sh
# 选项:sh install.sh --version v0.0.1 --prefix ~/.local --repo owner/gossh
```

脚本流程(所有平台一致):检测 OS/架构 → 下载平台压缩包(tar.gz)并校验 sha256 →
解压二进制到 `~/.local/bin` → 把该目录**幂等**写入 `~/.bashrc`
(同一行已存在则跳过,可安全重复执行)→ 提示 `source ~/.bashrc` 生效。

Windows 原生 PowerShell 安装(与 install.sh 同流程,注册 PATH 到 PowerShell
配置而非 .bashrc):

```powershell
powershell -ExecutionPolicy Bypass -File install.ps1
# 选项:-Version v0.0.1 -Prefix D:\tools -Repo owner/gossh
```

或源码构建(Go 1.26+,前端构建需 Node 18+ / pnpm):

```sh
make build      # 前端 + 静态资源 + ./build/gossh
make release    # 五平台矩阵 + sha256sums.txt
```

## 用法

### 服务

```sh
gossh serve                          # 默认 127.0.0.1:8040,打印带令牌的 URL
gossh serve --port 0                 # 随机端口
gossh serve --token my-token         # 固定令牌
gossh serve --timeout 3600           # 断开会话存活时间(秒),0 = 永不淘汰
gossh serve --ws-origin '^http://127\.0\.0\.1'   # 额外限制 WebSocket 来源
```

首启体验:

1. `gossh hosts add` 添加主机,或在浏览器「新建主机」表单里填写;
2. 点主机行「连接」→ 需要密码/密钥口令时输入,可选「保存到钥匙串」;
3. 页签里干活;SFTP、转发从页签工具栏进入。

### 桌面形态(Linux)

```sh
gossh app              # 托盘常驻 + 自动开浏览器(令牌自动注入 URL)
gossh app --no-browser # 只进托盘不弹浏览器(开机自启条目用这个)
```

- 服务与托盘同进程常驻:关浏览器不影响会话,托盘「退出」才停服销毁会话
- 重复运行 `gossh app` 只会打开已有实例的界面(单实例锁,`~/.gossh/app.lock`)
- 托盘菜单:**打开界面 / 开机自启(勾选)/ 退出**;自启状态 =
  `~/.config/autostart/gossh.desktop` 文件是否存在
- Linux 桌面用户建议直接用 Release 里的 **AppImage**:双击即用、免安装
- 托盘依赖 GTK/AppIndicator(cgo):`make build` 的 `CGO_ENABLED=0` 二进制运行
  `gossh app` 会提示改用 `gossh serve`;AppImage 与 CI 自带 cgo 构建
- 详见 [ADR 0006](docs/adr/0006-desktop-app.md)

### CLI

```sh
gossh hosts add --name prod --address 10.0.0.5 --user root --key ~/.ssh/id_ed25519
gossh hosts add --name bastion --address 1.2.3.4 --user ops
gossh hosts list
gossh hosts rm prod
gossh run prod 'uptime && df -h'     # 退出码透传;无浏览器可用
gossh version
```

## 安全模型

- 仅监听 `127.0.0.1`,随机访问令牌(`Authorization: Bearer` / `X-Gossh-Token` /
  `?token=`),常量时间比较;见 [ADR 0005](docs/adr/0005-access-token-posture.md)
- 密码/口令只进系统 keyring,永不落盘明文;keyring 不可用时不持久化
- 主机密钥 TOFU 校验每一跳(含跳板机);指纹不匹配拒绝连接
- 数据不离开本机进程;暴露到网络请自行加 TLS 反代并配合 `--ws-origin`

## 架构

```
internal/api        HTTP/WS 路由、令牌、主机/SFTP/转发/run 处理器
internal/session    会话注册表与生命周期(幂等创建、抢占、空闲淘汰,搬迁自 gotty)
internal/terminal   浏览器二进制帧协议(webtty,搬迁自 gotty)
internal/sshtty     session.Terminal 的 SSH 实现(远端 PTY shell)
internal/sshx       连接链拨号(任意深度跳板)、凭据解析、TOFU 信任库、keyring
internal/host       主机清单(hosts.json)与连接链解析
apps/web            Vue3 + Vite + xterm.js(页签/列表/SFTP)
```

详见 `docs/adr/`(0001-0005)与 `CONTEXT.md`(领域术语)。

## 开发

```sh
make install   # pnpm install
make build     # 前端 + static + ./build/gossh(SFTP/run 默认关闭)
make build SFTP=1 RUN=1    # 启用 SFTP 与单命令执行(Go tags + VITE_* 同源)
make test      # go vet + gofmt + go test(含搬迁自 gotty 的核心测试)
make release   # linux/amd64+arm64, darwin/amd64+arm64, windows/amd64
scripts/smoke.sh   # 对本地 sshd 的端到端冒烟(SFTP/run 步骤按编译开关跳过)
```

## 许可

MIT。
