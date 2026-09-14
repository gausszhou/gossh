# Agent 驱动接口与会话 CLI 设计

> 状态：已实现
> 关联：`internal/api/agent_handler.go`（`/screen`、`/wait`、`/keys`）、
> `internal/api/session_handler.go`（列表 / 标题 / 尺寸 / 信号）、
> `internal/session/session.go`（`Screen` / `Wait` / `Input`）、
> `internal/capture`（屏幕镜像渲染）、`cmd/session.go`（CLI 组）、
> `cmd/forwards.go`（会话级 / 主机级转发子命令）、
> `cmd/keys.go`（命名键表）、ADR-0005（访问令牌姿态）、ADR-0007（主机级转发常驻）
> 参考：terminal-use（MIT，https://github.com/flipbit03/terminal-use）

## 1. 背景与动机

浏览器是 gossh 的主界面，但会话本身运行在服务端进程里、与浏览器连接
解耦（关浏览器不断会话）。因此服务端天然具备成为「无人终端宿主」的条件：
一个程序（AI Agent、脚本、CI）完全可以不打开浏览器，直接驱动同一个会话。

为此服务端提供一组**面向程序**的 REST 接口——读屏、等待屏幕状态、注入输入
——本仓库称之为 **Agent 驱动接口（agent-driving API）**。数据源是服务端的
**屏幕镜像**（`--mirror`，默认开）：它在内存里用 VT 仿真器把会话 PTY 输出
解释成屏幕网格，从而让不附着终端的调用者也能「看到」当前画面。

单有 HTTP 接口对人和 shell 脚本并不友好（要拼 JSON、做 base64、记转义
字节），所以在接口之上再提供一个命令行客户端 `gossh session`。它的设计参照
[terminal-use](https://github.com/flipbit03/terminal-use) 的 `tu`，但有一处
关键差异：

| | terminal-use | gossh session |
|---|---|---|
| 会话宿主 | 自带常驻守护进程 | 已经跑着的 `gossh serve` / `gossh app` |
| CLI 角色 | 同进程 / IPC | **无状态 HTTP 客户端**，自己不建 PTY |
| 会话定位 | `--name`（默认 `default`） | `--session`（id / 唯一前缀 / 标题；省略即唯一存活会话） |
| 输出 | TTY 人读、管道 JSON | 同左 |
| 命名键 | CLI 翻译 | 同左（服务端只收原始字节） |

## 2. 接口契约

所有 `/api/*` 接口沿用 ADR-0005 的访问令牌（`Authorization: Bearer` /
`X-Gossh-Token` / `?token=`）。

### 2.1 `GET /api/sessions/{id}/screen?format=text|json|png`

返回**当下**这一帧的渲染结果。

- `text`（默认）：纯文本网格，`Content-Type: text/plain; charset=utf-8`。
- `json`：带样式的单元格与光标位置，见下。
- `png`：位图，`Content-Type: image/png`。
- 任意方式均带 `Cache-Control: no-store`。
- 屏幕镜像关闭时返回 `503`（`screen mirror disabled (start gossh with --mirror)`）。
- 会话不存在返回 `404`。

`json` 形态（`screenResponse`）：

```json
{
  "mirror": true,
  "session_id": "a1b2c3d4e5f6g7h8",
  "taken_at": "2026-09-11T03:26:23Z",
  "cols": 120,
  "rows": 30,
  "cursor": { "row": 3, "col": 12, "visible": true },
  "text": "…整屏文本…",
  "cells": [ { "r": 0, "c": 0, "ch": "$", "fg": "…", "bg": "…", "attr": "…" } ],
  "images": []
}
```

### 2.2 `POST /api/sessions/{id}/wait`

长轮询，直到满足条件之一或超时，返回**满足条件那一刻**的屏幕（与 §2.1 的
`json` 同形，另加三个布尔位）。

请求体：

```json
{ "regex": "password:", "timeout_ms": 30000, "quiet_ms": 0 }
```

- `regex` 与 `quiet_ms` **至少给一个**，否则 `400`；正则非法 `400`。
- `timeout_ms` 缺省 30s，上限 5 分钟（超出即截断）。
- 响应体在屏幕字段之外追加 `matched` / `quiet` / `timed_out`。
- 客户端中途断开（`context.Canceled`）不写响应。
- 镜像关闭返回 `503`。

### 2.3 `POST /api/sessions/{id}/keys`

向 PTY 写入**原始字节**，不附着任何客户端。

```json
{ "input": "ls -la\r", "encoding": "text" }
```

- `encoding`：`text`（默认，UTF-8 原样）或 `base64`（`input` 为 base64 编码）。
- 空 `input` 直接返回 `{"written": 0}`。
- 成功返回 `{"written": <字节数>}`；`encoding` 非法 `400`；会话已销毁 `409`。
- **服务端不做键名翻译**：`"Enter"` 这种名字由调用方负责转成 `"\r"`（见 §3.3）。
- `--permit-write=false` 的只读部署返回 `403`。

### 2.4 `GET /api/sessions`

列出服务端注册表里的全部会话，按 `created_at`→`id` 稳定排序，供 CLI 定位会话。

```json
{ "sessions": [ { "id": "…", "state": "…", "title": "…", "spec": { … }, "exited": false } ] }
```

（无会话时是 `{"sessions": []}`，不是 `null`。）

### 2.5 会话管理端点（`gossh session` 也使用）

这几个端点不属于 Agent 驱动语义（它们管理会话本身，不读屏也不注入输入），
但 CLI 的 `rename` / `resize` / `signal` / `forwards` 建立在它们之上，故一并
记在这里：

- `PUT /api/sessions/{id}/title`，体 `{"title": "…"}`；
- `POST /api/sessions/{id}/resize`，体 `{"width": <列>, "height": <行>}`
  （CLI 的 `--cols` / `--rows` 映射到 width / height）；
- `POST /api/sessions/{id}/signal`，体 `{"signal": "SIGTERM"}`。这是给会话
  背后的进程发信号，与 `DELETE /api/sessions/{id}`（销毁会话）是两回事；
  本地服务器会话在 Windows 上只有终止类信号有语义，其余静默 no-op（ADR-0008）；
- `GET` / `POST /api/sessions/{id}/forwards`、`DELETE …/forwards/{fid}`：
  会话级临时转发，跑在会话自己的 SSH 连接上——本地服务器会话没有 SSH
  连接，因此返回明确错误而不是静默失败。

> 主机级常驻转发不在会话端点上：配置写在主机记录里（`PUT /api/hosts/{id}`），
> 运行状态见 `GET /api/hosts/{id}/forwards`（running/pending/failed/disabled），
> 详见 ADR-0007 与 `gossh hosts forwards`。

## 3. 会话 CLI（`gossh session`）

### 3.1 定位与端点解析

- 地址与令牌来源与 `gossh serve` 一致：先读同一份配置文件（默认
  `~/.gossh/config.json`），再依次被 `--server` / `GOSSH_SERVER`、
  `--token` / `GOSSH_TOKEN` / `--token-file` 覆盖。默认安装无需任何参数。
- `--port 0`（随机端口）无法从配置推断地址，报错要求显式 `--server`。
- 会话定位 `resolveSession`：完整 id → 唯一 id 前缀 → 标题/主机名；前缀或
  名字命中多个则报歧义并列出候选；**省略 `--session` 时若恰好只有一个存活
  会话则自动选中**，多个则列出并要求指定。

### 3.2 输出约定

`cliJSON` 判定规则：`--json` 显式指定则以其为准，否则「stdout 是 TTY → 人读、
被管道/重定向 → JSON」。因此 agent 直接 `... | jq` 就能拿到结构化结果，无需
额外参数。唯一例外是 `screen --png`：图像没有 JSON 形态，`--png` 抑制默认
JSON，`screen --png > shot.png` 得到的就是纯位图。

### 3.3 命名键（`cmd/keys.go`）

`press` 接受命名键并在**客户端**翻译成字节，服务端保持「只收原始字节」的
简洁契约。名称与 terminal-use 对齐：

| 名称 | 字节 | 名称 | 字节 |
|---|---|---|---|
| `Enter` | `\r` | `Up` | `\x1bOA` |
| `Tab` | `\t` | `Home` / `End` | `\x1bOH` / `\x1bOF` |
| `Escape` | `\x1b` | `F1`–`F4` | `\x1bOP`…`\x1bOS` |
| `Backspace` | `\x7f` | `F5`–`F12` | `\x1b[15~`…`\x1b[24~` |
| `Delete` / `Insert` | `\x1b[3~` / `\x1b[2~` | `Ctrl+C` | `\x03` |
| `Alt+f` | `\x1b` + `f` | `Shift+Tab` | `\x1b[Z` |

方向键用 SS3 形式（`ESC O A`）、F1–F4 用 SS3，与 `xterm-256color` 的
terminfo 一致；带修饰的导航键/功能键用 `CSI 1;{mod}{final}` /
`CSI {code};{mod}~`（`mod`：2=Shift 3=Alt 4=Shift+Alt 5=Ctrl 6=Ctrl+Shift
7=Ctrl+Alt 8=Ctrl+Shift+Alt）。任意单字符（含非 ASCII）解析为其自身 UTF-8
字节。`press` 一律走 base64 发送，保证序列精确、即使不是合法 UTF-8。

### 3.4 命令一览

| 命令 | 作用 | 关键参数 |
|---|---|---|
| `ls` | 列出会话 | — |
| `create` | 建会话并打印 id | `--host`（id/名，`local` 为本机）、`--id`、`--password`/`--passphrase`、`--save` |
| `screen` | 读屏 | `--png`、`-o/--output`、`--stdout` |
| `wait` | 阻塞到匹配/静止 | `--text`、`--stable`、`--timeout` |
| `type` | 输入字面文本 | `--enter` |
| `press` | 发送命名键 | 位置参数为键名序列 |
| `rename` | 设置会话标题（持久化在服务端） | 位置参数为标题 |
| `resize` | 调整会话 PTY 尺寸（屏幕 / PNG 也按此渲染） | `--cols`、`--rows` |
| `signal` | 给会话背后的进程发信号（不是销毁会话） | 位置参数为信号名（`SIGTERM`/`SIGINT`/`SIGHUP`/`SIGQUIT`/`SIGKILL`） |
| `forwards` | 会话自己的临时端口转发 | `ls`；`add --kind --bind [--target]`；`rm <forward-id>` |
| `destroy` / `kill` | 销毁会话（记录留作历史） | — |
| `usage` | 一屏打印参考（面向 LLM） | — |

全局参数：`-s/--session`、`--json`、`--server`、`--token`、`--token-file`。

### 3.5 前提与失败提示

- 读屏类（`screen`、`wait`）依赖屏幕镜像（`--mirror`，默认开）；关闭时服务端
  `503`，CLI 附加提示。
- 写入类（`type`、`press`）依赖 `--permit-write`（默认开）；只读部署 `403`，
  CLI 提示「server was started with --permit-write=false」。
- `401` 提示检查 `--token` / `GOSSH_TOKEN` / 令牌文件。
- `wait` 超时：向 stderr 打印错误并 **exit 1**（成功时静默 exit 0），便于作为
  同步原语组合进脚本。

## 4. 边界与异常

| 场景 | 行为 |
|---|---|
| 无任何会话 | `resolveSession` 报「no sessions」，提示先 `create` |
| 多个存活会话且未指定 | 列出候选并要求 `--session` |
| `--session` 命中多个前缀/名字 | 报歧义并列出 |
| `create --host` 名重复 | 报错要求用 host id |
| 服务未启动/地址错 | `cannot reach <base>` |
| `--png` 与 `--json` 同时给 | 直接报错（不发请求） |
| `wait` 无 `--text`/`--stable` 或 `--timeout` 越界 | 直接报错（不发请求） |

## 5. 测试

- 单元：`cmd/keys_test.go`（对照 terminal-use 的字节序列、未知键拒绝、序列
  拼接、不别名表内切片）；`cmd/session_test.go`（端点解析、会话定位、ls、
  create、press/type、screen 三种格式与互斥、wait 静默/超时、destroy、鉴权
  失败、表格渲染）；`internal/api/session_list_test.go`（列表排序、空列表、
  鉴权）。
- 端到端：对一个真实运行的 `gossh serve` 跑通建会话 → `type` → `wait` →
  `screen`（text/json/png）→ `press Up Enter`（回溯历史证明命名键字节到达
  PTY）→ `destroy`，并覆盖超时/未知键/错误目标/错误令牌等失败路径。
