# GoSSH — 浏览器里的 SSH 客户端

[English](README.md) | **简体中文**

一个基于 Go 技术栈的 SSH 客户端:本地运行一个服务端,浏览器就是你的终端 UI。
管理主机清单、多会话页签、端口转发、凭据入库(keyring)。

![GoSSH 界面](assets/demo.gif)

```
gossh serve
# HTTP server is listening at: http://127.0.0.1:8040
# Open the page with the access token:
#   http://127.0.0.1:8040/?token=4f0a...
```

打开浏览器地址即可使用。整个服务只监听本机,令牌护体——私钥不出进程。

## 特性

- **主机清单**:CRUD
- **多会话页签**:SSH 会话页签可左右拖拽排序,顺序按设备持久化
  (localStorage `gossh.tabOrder`)
- **凭据**:私钥文件路径引用、ssh-agent、密码;密码与密钥口令经系统 keyring
  (Linux Secret Service / macOS Keychain / Windows Credential Manager)加密保存,
  无 keyring 守护进程时自动回退为内存保存
- **主机密钥**:TOFU 信任管理(`~/.gossh/known_hosts`),指纹变更即拒绝连接
- **端口转发**:local / remote / dynamic(SOCKS5);主机级转发跑在主机专属的
  转发连接上,**不随会话生灭**——关终端页签/销毁会话转发仍在
  (见 [ADR 0007](docs/adr/0007-host-forwards-resident.md))
- **本地服务器**:主机清单首位的常驻条目(127.0.0.1),连接即在运行 gossh 的
  机器上开一个本地终端——本机 PTY,不经 SSH、无需凭据;只能连接,不可编辑/
  转发/删除(见 [ADR 0008](docs/adr/0008-local-server.md))
- **断开存活**:浏览器断开或刷新,SSH 会话继续存活,空闲超时(默认 900s)后销毁
- **单二进制交付**:前端经 `go:embed` 内嵌,跨平台编译无需 Node 运行时
- **桌面形态(Linux + Windows)**:`gossh app` 托盘常驻、开机自启、单实例;自动开浏览器并
  注入令牌;Linux Release 附带免安装的 AppImage,Windows 用默认二进制即可

## 安装

```sh
curl -fsSL https://raw.githubusercontent.com/gausszhou/gossh/main/scripts/install.sh | sh
# 选项:sh install.sh --version v0.0.1 --prefix ~/.local --repo owner/gossh
```

脚本流程(所有平台一致):检测 OS/架构 → 下载平台压缩包(tar.gz)并校验 sha256 →
解压二进制到 `~/.local/bin` → 把该目录**幂等**写入 `~/.bashrc`
(同一行已存在则跳过,可安全重复执行)→ 若已存在 `~/.profile` 则幂等补一行
`. ~/.bashrc`(保证 Git Bash 等 login shell 也加载 `~/.bashrc`;仅当其未引用
bashrc 时才追加)→ 提示 `source ~/.bashrc` 生效。

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
2. 鼠标移到主机行上,点 ▶ 连接 → 需要密码/密钥口令时输入,可选「保存到钥匙串」;
3. 页签里干活;端口转发与编辑/删除在主机行的行内按钮与其右键菜单里。

### 桌面形态

```sh
gossh app              # 托盘常驻 + 自动开浏览器(令牌自动注入 URL)
gossh app --no-browser # 只进托盘不弹浏览器(开机自启条目用这个)
```

- 服务与托盘同进程常驻:关浏览器不影响会话,托盘「退出」才停服销毁会话
- 重复运行 `gossh app` 只会打开已有实例的界面(单实例:Linux 为
  `flock ~/.gossh/app.lock`,Windows 为命名互斥体 `Local\gossh-app-<用户>`)
- 托盘菜单:**打开界面 / 开机自启(勾选)/ 退出**
  - Linux:自启状态 = `~/.config/autostart/gossh.desktop` 文件是否存在
  - Windows:自启状态 = HKCU `...\CurrentVersion\Run` 下的 `GoSSH` 值;
    **默认 `make build` 的 windows 二进制即可跑 `gossh app`**——托盘是纯 Go
    的 win32 消息循环,无需 cgo、无额外运行时
- Linux 桌面用户建议直接用 Release 里的 **AppImage**:双击即用、免安装
- Linux 托盘依赖 GTK/AppIndicator(cgo):`CGO_ENABLED=0` 二进制运行
  `gossh app` 会提示改用 `gossh serve`;AppImage 与 CI 自带 cgo 构建
- 详见 [ADR 0006](docs/adr/0006-desktop-app.md)

### CLI

下面每个命令组都驱动一个**正在运行**的服务端(`gossh serve` / `gossh app`)——
主机清单、信任库、keyring 与会话都由服务端持有,因此 CLI 是无状态客户端,
不绕过运行中的服务端去改它底下的文件。地址与令牌取自 `gossh serve` 读的同一份
配置文件(默认 `~/.gossh/config.json`,故默认安装无需任何参数);`--server` /
`--token`(或 `GOSSH_SERVER` / `GOSSH_TOKEN`)可覆盖。TTY 上是人读表格,被管道
或重定向时是 JSON,`... | jq` 直接可用。

主机清单:

```sh
gossh hosts ls                                  # 内置本地服务器是第一行
gossh hosts add --name prod --address 10.0.0.5 --user root --key ~/.ssh/id_ed25519
gossh hosts add --name bastion --address 1.2.3.4 --user ops --password --secret -
gossh hosts show prod                           # 按 id 或名字
gossh hosts edit prod --name production         # 只改你显式传的字段
gossh hosts rm prod
gossh version
```

`--secret -` 从 stdin 读密钥,不给就不写 keyring;直接写在命令行上的 `--secret`
值会出现在进程列表里。

主机级常驻端口转发(配置写在主机记录上,由主机自己的转发连接承载——不随会话
生灭,见 [ADR 0007](docs/adr/0007-host-forwards-resident.md)):

```sh
gossh hosts forwards ls   prod                  # 配置 + 运行状态(running/pending/failed)
gossh hosts forwards add  prod --kind local --bind 127.0.0.1:8080 --target localhost:80
gossh hosts forwards rm   prod --bind 127.0.0.1:8080
```

信任库、keyring 与全站页面标题:

```sh
gossh known-hosts ls                            # 已固定的 TOFU 指纹
gossh known-hosts forget 10.0.0.5:22            # 忘记它 → 下次连接重新首连信任
gossh secrets set --kind password --addr 10.0.0.5:22 --user root --secret -
gossh secrets set --kind passphrase --key ~/.ssh/id_ed25519 --secret -
gossh secrets rm  --kind password --addr 10.0.0.5:22 --user root
gossh title get
gossh title set "My fleet"
gossh title clear                               # 回到内置标题
```

#### 驱动会话(`gossh session`)

`gossh session` 是 [Agent 驱动接口](docs/design/agent-driving-api.md)
(`GET /screen`、`POST /wait`、`POST /keys`)的命令行入口。它是一个**无状态、
一次性**的 HTTP 客户端——会话住在你已经跑起来的服务端(`gossh serve` /
`gossh app`)里,CLI 自己不开 PTY。设计参考
[terminal-use](https://github.com/flipbit03/terminal-use):`--session` 定位会话、
命名键、以及「TTY 出人读文本、管道出 JSON」。

```sh
gossh session ls                          # 列出运行中服务端的会话
gossh session create --host prod          # 连一台主机,打印会话 id
gossh session type "ls -la" --enter       # 输入文本(--enter 回车提交)
gossh session wait --text 'password:'     # 阻塞直到屏幕匹配
gossh session screen                      # 读取当前渲染的屏幕
gossh session screen --png -o shot.png    # 渲染成 PNG
gossh session press Ctrl+C                # 发送命名键
gossh session press Escape : w q Enter    # 作为一整段序列发送
gossh session rename deploy-window        # 标题,持久化在服务端
gossh session resize --cols 200 --rows 50 # 调整 PTY 尺寸(屏幕/PNG 也按此渲染)
gossh session signal SIGTERM              # 给会话进程发信号
gossh session forwards ls                 # 会话自己的临时转发
gossh session forwards add --kind local --bind 127.0.0.1:9000 --target localhost:80
gossh session forwards rm <forward-id>
gossh session destroy                     # 别名:kill
gossh session usage                       # 一屏完整参考
```

- **定位会话。** `-s/--session` 接受完整 id、唯一 id 前缀或标题/主机名。
  省略时默认作用于唯一存活的会话;若有多个存活会话,CLI 会列出并提示指定。
- **输出约定。** 在 TTY 上是人读的表格/纯文本;被管道或重定向时输出 JSON,
  agent 无需额外参数即可获得结构化结果。`--json` 可强制任一模式
  (`screen --png` 是唯一例外——图像没有 JSON 形态)。
- **命名键。** `type` 发送字面文本;`press` 发送命名键——`Enter`、`Tab`、
  `Escape`、方向键、`PageUp`/`PageDown`、`F1`–`F12`、修饰组合(`Ctrl+C`、
  `Alt+f`、`Shift+Tab`、`Ctrl+Shift+Up`)以及任意单字符。服务端刻意只收
  **原始字节**(不做键名翻译),映射放在客户端。
- **前提。** 读屏需要服务端的屏幕镜像(`--mirror`,默认开);写入需要
  `--permit-write`(同样默认开)。只读部署会以 403 拒绝写入,CLI 会给出提示。
- **地址与令牌**取自 `gossh serve` 读的同一份配置文件(默认
  `~/.gossh/config.json`),因此默认安装无需任何参数;`--server` / `--token`
  (或 `GOSSH_SERVER` / `GOSSH_TOKEN`)可覆盖。
- **两级端口转发。** `gossh session forwards` 是临时的,跑在会话自己的 SSH
  连接上(本地服务器会话没有 SSH 连接,CLI 会明确报错);`gossh hosts
  forwards` 写在主机记录上,不随会话生灭。
- **`signal` 不是 `destroy`。** `gossh session signal SIGTERM` 是给会话背后的
  进程发信号;`gossh session destroy`(别名 `kill`)是在服务端把整个会话销毁。

典型的 Agent 驱动循环:

```sh
id=$(gossh session create --host prod --json | jq -r .id)
gossh session wait    -s "$id" --text 'login:' --timeout 20000
gossh session type    -s "$id" 'deploy' --enter
gossh session wait    -s "$id" --stable 2000
gossh session screen  -s "$id"
gossh session destroy -s "$id"
```

CLI 背后封装的 HTTP 契约见
[Agent 驱动接口参考](docs/design/agent-driving-api.md)。

## 安全模型

- 仅监听 `127.0.0.1`,随机访问令牌(`Authorization: Bearer` / `X-Gossh-Token` /
  `?token=`),常量时间比较;见 [ADR 0005](docs/adr/0005-access-token-posture.md)
- 密码/口令只进系统 keyring,永不落盘明文;keyring 不可用时不持久化
- 主机密钥 TOFU 校验;指纹不匹配拒绝连接
- 数据不离开本机进程;暴露到网络请自行加 TLS 反代并配合 `--ws-origin`
- 内置的本地服务器会在运行 gossh 的机器上执行命令:持有访问令牌者即可用该
  用户身份执行任意命令。保持只监听回环的默认值;可用 `GOSSH_LOCAL_SHELL`
  指定本地 shell

## 架构

```
internal/api        HTTP/WS 路由、令牌、主机/转发处理器
internal/session    会话注册表与生命周期(幂等创建、抢占、空闲淘汰,搬迁自 gotty)
internal/terminal   浏览器二进制帧协议(webtty,搬迁自 gotty)
internal/sshtty     session.Terminal 的 SSH 实现(远端 PTY shell)
internal/localtty   session.Terminal 的本地服务器实现(本机 PTY:Unix /dev/ptmx、Windows ConPTY)
internal/sshx       直连拨号、凭据解析、TOFU 信任库、keyring
internal/host       主机清单(hosts.json)
apps/web            Vue3 + Vite + xterm.js(页签/主机列表)
```

详见 `docs/adr/`(0001-0008)、`CONTEXT.md`(领域术语)与
`docs/design/`(Agent 驱动接口、WebSocket 多路复用、UI 样式指导)。

## 开发

```sh
make install   # pnpm install
make build     # 前端 + static + ./build/gossh
make test      # go vet + gofmt + go test(含搬迁自 gotty 的核心测试)
make release   # linux/amd64+arm64, darwin/amd64+arm64, windows/amd64
scripts/build-local.ps1   # Windows:一键构建 + 安装到 ~/.local/bin
scripts/smoke.sh   # 对本地 sshd 的端到端冒烟
```

## 许可

MIT。
