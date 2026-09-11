# 本地服务器:本机 PTY 会话,不经 SSH

主机清单首位常驻一条内置条目「本地服务器」(127.0.0.1):点击连接即在运行 gossh 的那台机器上开一个本地 PTY shell。该条目只提供连接,不可编辑、不可转发、不可删除。

## 背景

产品此前只有一种会话来源:主机清单里的主机 → SSH 拨号 → 远端 PTY(`sshtty`)。用户诉求:「主机列表中增加一个默认的本地服务器 127.0.0.1,只有连接按钮,不能编辑转发删除」。

「只有连接」不是 UI 简化,而是领域事实的直接后果:本地服务器没有主机记录与凭据可编辑,没有 SSH 连接可承载端口转发,也不是清单记录可删除——它只是本机上的一台终端。

## 决策

- **本地服务器不是主机记录**:`host.Local()` 返回一条虚拟记录(`builtin=true`),不落 `hosts.json`;`Inventory.List()` 不返回它,`Add/Update/Remove` 一律返回 `ErrBuiltin`,只有 `Get` 解析它——会话创建路径(host id → ConnectSpec)因此与真实主机共用同一条代码路径,`GET /api/hosts` 在列表首位显式拼上它。
- **预留 id `local`**:用户主机 id 恒为 `h_<nanos>_<suffix>`,不会与之冲突。
- **会话工厂按 spec 分派**:`api.dialFactory` 见到 `host.IsLocal(spec.HostID)` 走 `localtty.New`(本机 PTY),否则走原 SSH 链路。`session.Terminal` 接口不变,因此会话管理器、WS 附着、屏幕镜像与 agent 读屏 API 全部复用。
- **本地 PTY 自己实现,不加依赖**:Unix 走 `/dev/ptmx` + `TIOCSPTLCK`/`TIOCGPTN`(Linux)或 `TIOCPTYGRANT`/`TIOCPTYUNLK`/`TIOCPTYGNAME`(Darwin),Windows 走 ConPTY(`golang.org/x/sys/windows` 已提供 `CreatePseudoConsole`/`ResizePseudoConsole`)。shell 取 `GOSSH_LOCAL_SHELL`,否则 Windows 依次尝试 `pwsh.exe`/`powershell.exe`/`%COMSPEC%`,Unix 取 `$SHELL`/`/bin/sh`。
- **转发与凭据链路显式跳过**:会话建立时不 `ensure` 主机级转发;`dialHostForward` 对本地 id 直接报错;本地终端不实现 `SSHClient()`,会话级转发/SFTP 因此拿到「session has no ssh connection」而不是 panic。
- **重启后照旧复活**:本地会话与 SSH 会话一样记 `Metadata.Spec`(hostId=`local`),浏览器重连/服务重启后按同一 spec 重建,起一个新的本地 shell。

## 理由

- 「本地服务器是终端,不是连接」这一句能同时解释三条 UI 限制;反过来若把它做成一条指向 127.0.0.1 的普通主机记录,「不能转发」就变成了无理由的限制。
- 复用 `session.Terminal` 抽象意味着镜像、抢占、空闲淘汰、输入门控(重放握手)等既有语义对新终端类型自动成立——这些是 gotty 移植中最难的部分。
- 不引入 `creack/pty` 等依赖:所需 ioctl 与 ConPTY 绑定在 `golang.org/x/sys` 里已有,`CGO_ENABLED=0` 的发布矩阵不受影响。

## 代价

- **本地命令执行面**:持有访问令牌者可经浏览器在本机执行任意命令。默认仅监听 `127.0.0.1` + 随机令牌(ADR-0005)是唯一屏障;暴露到网络必须自行加 TLS 反代与 `--ws-origin`,并在文档中明确(见 README 安全模型)。
- Windows 上 `Signal` 无 POSIX 语义:终止类信号(SIGHUP/SIGINT/SIGTERM/SIGKILL)映射为 `TerminateProcess`,其他信号为 no-op。
- 会话级端口转发、SFTP 对本地会话不可用(无 SSH 连接),API 返回明确的错误信息而非静默失败。

## 备选

- **SSH 连本机 127.0.0.1**:复用现有链路,但要求本机跑着 sshd,且内置条目无从取用户名/凭据(用户与凭据属于主机记录);「不能转发」也失去依据。否决。
- **把本地主机写进主机清单(种子记录)**:清单是用户数据的唯一事实来源,系统往里塞一条不可删的记录会污染 `hosts.json` 与 `gossh hosts` CLI 的输出。否决。
- **不做本地 shell,只做一条普通主机记录**:那只是用户的日常操作(自己加一条 localhost 主机),不构成产品特性。
