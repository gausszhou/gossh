# GoSSH — 浏览器里的 SSH 客户端

一个基于 Go 技术栈的 SSH 客户端:本地运行一个服务端,浏览器就是你的终端 UI。
管理主机清单、多会话页签、SFTP 文件传输、端口转发、跳板链、凭据入库(keyring)。

> English documentation: [README.md](README.md).

```
gossh serve
# HTTP server is listening at: http://127.0.0.1:9049
# Open the page with the access token:
#   http://127.0.0.1:9049/?token=4f0a...
```

打开浏览器地址即可使用。整个服务只监听本机,令牌护体——私钥不出进程。

## 特性

- **主机清单**:CRUD、分组、搜索、`via` 跳板链(任意深度,ProxyJump 语义)
- **多会话页签**:SSH 会话 / SFTP 浏览 / 单命令执行结果三类页签并存,
  页签可左右拖拽排序,顺序按设备持久化(localStorage `gossh.tabOrder`)
- **凭据**:私钥文件路径引用、ssh-agent、密码;密码与密钥口令经系统 keyring
  (Linux Secret Service / macOS Keychain / Windows Credential Manager)加密保存,
  无 keyring 守护进程时自动回退为内存保存
- **主机密钥**:TOFU 信任管理(`~/.gossh/known_hosts`),指纹变更即拒绝连接
- **端口转发**:local / remote / dynamic(SOCKS5)
- **断开存活**:浏览器断开或刷新,SSH 会话继续存活,空闲超时(默认 900s)后销毁
- **单命令执行**:`gossh run <host> '<cmd>'` 无浏览器直跑,浏览器内也有入口
- **单二进制交付**:前端经 `go:embed` 内嵌,跨平台编译无需 Node 运行时

## 安装

```sh
curl -fsSL https://raw.githubusercontent.com/gausszhou/gossh/master/scripts/install.sh | sh
# 选项:sh install.sh --version v0.0.1 --prefix ~/.local --repo owner/gossh
```

或源码构建(Go 1.26+,前端构建需 Node 18+ / pnpm):

```sh
make build      # 前端 + 静态资源 + ./build/gossh
make release    # 五平台矩阵 + sha256sums.txt
```

## 用法

### 服务

```sh
gossh serve                          # 默认 127.0.0.1:9049,打印带令牌的 URL
gossh serve --port 0                 # 随机端口
gossh serve --token my-token         # 固定令牌
gossh serve --timeout 3600           # 断开会话存活时间(秒),0 = 永不淘汰
gossh serve --ws-origin '^http://127\.0\.0\.1'   # 额外限制 WebSocket 来源
```

首启体验:

1. `gossh hosts add` 添加主机,或在浏览器「新建主机」表单里填写;
2. 点主机行「连接」→ 需要密码/密钥口令时输入,可选「保存到钥匙串」;
3. 页签里干活;SFTP、转发从页签工具栏进入。

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
make build     # 前端 + static + ./build/gossh
make test      # go vet + gofmt + go test(含搬迁自 gotty 的核心测试)
make release   # linux/amd64+arm64, darwin/amd64+arm64, windows/amd64
```

## 许可

MIT。基础代码搬迁自 [gotty](https://github.com/gausszhou/gotty)(原作者 Iwasaki Yudai),
版权声明见 LICENSE。
