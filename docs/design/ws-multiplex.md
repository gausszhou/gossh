# WebSocket 连接复用与二进制路由协议设计

> 状态：已实现（v0.3.0 后的当前协议）
> 关联：`internal/api/ws_handler.go`（`handleWS` 分派两种协议、`wsRouter`
> 与 `virtualConn` 实现多路复用）、
> `internal/session/session.go`（附着与抢占状态机）、
> `internal/terminal/protocol.go`（帧编解码，含 §2.1 的路由帧）、
> `apps/web/src/utils/multiplexer.ts`（浏览器侧单连接）、
> `apps/web/src/utils/session-channel.ts`（浏览器侧逻辑通道 + 编解码）、
> ADR-0001（会话 id 由客户端生成）、ADR-0002（用 Web UI 承载会话管理）、
> ADR-0005（访问令牌姿态）
>
> **与初稿的一处偏差**：§4 原计划「旧的 `/ws?session_id=xxx` 端点移除」，
> 实际采用**双协议并存**——带 `?session_id=` 走旧的单会话路径，不带则走
> 多路复用。前端是本仓库唯一消费者，等新路径稳定后再删旧协议（见 §9）。

## 1. 背景与动机

当前前端为每个会话建立一条 WebSocket（`/ws?session_id=xxx`，每会话单
连接），存在两个问题：

1. **浏览器同域 WebSocket 连接数上限**（约 6 条）：前端升级为
   "v-show 常驻视图"后，每个打开过的会话各占一条连接，实际同时打开
   的会话上限被浏览器卡死，与 `--max-session 10` 的模型冲突。
2. **连接与视图生命周期强耦合**：切换页签（v-show）时连接需常驻，
   但 N 个会话 = N 条 TCP 连接的开销与上限都不划算。

**目标**：浏览器与 GoTTY 之间只保留 **1 条 WebSocket 连接**，连接内
通过二进制帧携带 `session_id` 进行多路路由；会话的"单连接 + 抢占"
语义从"物理连接"提升为"连接内逻辑通道"。

## 2. 协议设计

### 2.1 帧格式

```
+------------------+------+-------+-------------+
| session_id (16B) | type | len(2B BE) | payload |
+------------------+------+-------+-------------+
```

- `session_id`：会话标识，固定 **16 字节 ASCII**（base36 小写 `0-9a-z`。
  由客户端生成，见 ADR-0001；服务端只在缺省时用 `utils.RandomString(16)`
  兜底，入参校验见 `utils.IsValidSessionID`）。`session_id` 全 `0x00`
  表示**连接级消息**。
- `type`：1 字节消息类型（复用既有 webtty 消息类型：`'1'`~`'5'`，
  新增 `'A'`/`'D'`/`'a'`/`'b'`/`'E'`/`'0'`）。
- `len`：payload 长度，2 字节大端（payload ≤ 65535）。
- WebSocket 帧类型仍为 BinaryMessage，子协议仍为 `webtty`。

### 2.2 消息类型

| 层级 | type | 方向 | payload | 说明 |
|---|---|---|---|---|
| 连接级 | `'2'` (0x32) | C→S / S→C | — | Ping / Pong（保活 + RTT 测量） |
| 会话级 | `'A'` (0x41) | C→S | 可选 JSON `{cols,rows}` | **Attach**：附着到指定会话（抢占语义见 §3） |
| 会话级 | `'D'` (0x44) | C→S | — | **Detach**：主动分离（关闭视图时发送） |
| 会话级 | `'a'` (0x61) | S→C | — | Attach OK（此后开始推帧） |
| 会话级 | `'b'` (0x62) | S→C | 原因短语 | Attach 失败（会话不存在/已销毁） |
| 会话级 | `'E'` (0x45) | S→C | 1 字节 status | 会话事件：`0x01`=被抢占，`0x02`=已销毁 |
| 会话级 | `'1'` (0x31) | C→S / S→C | 原始字节 | Input / Output |
| 会话级 | `'3'` (0x33) | C→S / S→C | JSON / 字符串 | ResizeTerminal / SetWindowTitle |
| 会话级 | `'4'` (0x34) | S→C | JSON | SetPreferences |
| 会话级 | `'5'` (0x35) | S→C | JSON | SetReconnect |
| 会话级 | `'6'` (0x36) | S→C | — | SetReplayDone（附着时输出尾部重放结束的握手标记，客户端据此开启输入上行） |

> 旧单会话协议中"会话由 URL `?session_id=` 指定"的语义取消，改为
> Attach 帧显式声明。其余消息类型字节与旧协议完全一致，会话层编解码
> （`internal/terminal/protocol.go` 的 EncodeFrame/DecodeClientFrame
> 与 `Session.bridge`）不需要改动，只在其上加一层路由头。

### 2.3 连接生命周期

```
客户端                         服务端
  │── WS connect (subprotocol webtty) ──→│
  │── '2' Ping(5s 周期) ────────────────→│  RTT = 收到 Pong 的时间差
  │◄── '2' Pong ────────────────────────│
  │── 'A' sid1 ─────────────────────────→│  Attach
  │◄── 'a' sid1 ────────────────────────│  随后推 title/prefs/reconnect
  │── 'A' sid2 ─────────────────────────→│  可并发附着多个会话
  │                ...
  │── WS 关闭 ──────────────────────────→│  所有 Attach 返回,全部会话 → IDLE
```

> 访问控制沿用 ADR-0005:访问令牌(`?token=` / `X-Gossh-Token`)与
> ws-origin 校验都在握手前完成(见 `internal/api/auth.go` 与
> `mux.Handle("GET /ws", ...)`),复用了单会话协议时的同一道门;协议层
> 因此不需要再携带任何凭据。

### 2.4 延迟测量

Ping 仍为**连接级**消息（前端现为 2 秒周期，见
`apps/web/src/utils/ws.ts`），RTT 由浏览器
`performance.now()` 差值实测，展示在激活页签标题左侧（既有功能，
不受多路复用影响）。

## 3. 会话附着与抢占

### 3.1 虚拟连接（SessionChannel）

每条 Attach 对应一个**逻辑通道**（"虚拟连接"），实现既有
`io.ReadWriter` 接口，插入 `Session.Attach(ctx, conn, opts)` 不变：

- **读**：从单连接收帧循环按 `session_id` 分发到该通道的队列，
  通道 `Read(p)` 取出一段完整会话级帧（`[type][payload]`）。
- **写**：通道 `Write(b)` 将 `b[0]` 作为 type、`b[1:]` 作为 payload，
  包裹 `[sid]` 路由头后写回单条 WS。

### 3.2 单会话单"连接"与抢占

沿用既有规则（每会话同窗口期只有 1 个附着者，新附着抢占旧附着）：

1. `Session.Attach` 锁内检查：若会话已 `RUNNING`（被其他连接附着），
   **新 Attach 立即接管**：关闭旧通道（`io.Closer`），`attachEpoch++`
   作为所有权令牌；
2. 旧通道被关闭 → 旧桥接循环退出，其 `defer` 仅在**自己仍是当前
   owner**（`attachEpoch` 未变）时才把状态退回 `IDLE` —— 杜绝抢占瞬间
   旧 Attach 误改新 Attach 状态；
3. 被抢占的旧通道在其同一 WS 连接上收到 `'E' status=0x01`
   （被抢占）事件帧；前端据此显示"会话已被其他客户端接管"弹窗，
   **且不自动重连**（防止两个客户端来回抢占死循环）；
4. 会话已销毁：Attach 返回 `'b'`（原因"会话已销毁/不存在"）。

> 注意:抢占的"双读窗口"在既有实现里已经不存在 —— 会话自创建起就
> 只有一个 PTY 读循环(`Session.outputPump`,读归属会话;客户端连接只是
> 可替换的写槽位,见 `internal/session/session.go`),不会出现两个
> goroutine 同时读 PTY、抢占瞬间丢输出。本步只需在其上加一层按 `sid`
> 的路由头。

## 4. 服务端架构

```
internal/api/ws_handler.go
  └── handleWS: 单连接收帧循环
        ├── 连接级: 认证、Ping→Pong
        ├── Attach:  (复用会话不存在→'b';存在→go session.Attach(通道))
        ├── Detach:  关闭该通道
        └── 会话级帧: 按 sid 投递到通道队列
  每条 ws 连接一个 wsRouter: map[sid]*virtualConn + 写锁
```

- `handleWS` 从"每连接处理一个会话"改为按 URL 分派：带 `?session_id=`
  走 `handleWSSingle`（旧路径，一行未改），不带则交给
  `handleWSMultiplexed`（`wsRouter` 管理 N 个会话通道）；
- 会话注册表/状态机/历史持久化（REST 侧）完全不变；
- 旧的 `/ws?session_id=xxx` **暂时保留**（双协议并存），待多路复用稳定后
  删除 —— 见 §9。子协议名 `webtty` 两种路径共用。

## 5. 前端架构

```
utils/multiplexer.ts          单 WS 连接:令牌/帧编解码/Ping/按 sid 分发
                                 attach(sid)→SessionChannel;'E'/'b' 回调
utils/session-channel.ts      路由帧编解码 + 每个 attach 一条逻辑通道
                                 (SessionChannel:按 sid 编码写、事件回调)
utils/ws.ts                   在会话通道上收发:输入/输出/重连/上行输入
                                 门控/延迟展示(延迟改由共享连接统一测量)
components/TerminalPane.vue   改用全局 Multiplexer.attach(sid) 建立通道,
                                 断开/抢占/销毁事件 → 既有弹窗状态机
```

- 全局唯一 `Multiplexer`（每页一条 WS），多个常驻 `TerminalPane`
  共享；没有会话占用时连接会被收掉，下一个 attach 再开一条；
- 连接级断线 → 所有通道 `onClose` → 各视图弹"连接已断开"；
- 被抢占 → `'E' 0x01` → "会话已被其他客户端接管"弹窗，且**不自动重连**
  （否则两个客户端互相踢）；
- 会话销毁（`'E' 0x02`）→ `onGone` → "会话已销毁"弹窗。

> 初稿写的"被抢占 → `onClose(4000)`"没有落地：多路复用下不能靠关闭码
> 表达单个会话的命运（一条连接上还有别的会话），改用了 `'E'` 事件帧。
> 延迟也从"每会话一个 Ping"改成由共享连接统一测量后广播给所有通道——
> RTT 本来就是这条连接的属性。

## 6. 边界与异常

| 场景 | 行为 |
|---|---|
| 服务重启 / 网络断开 | 单连接断 → 所有视图弹断开弹窗（可重新连接/自动重连，既有机制） |
| Attach 不存在的会话 | `'b'` 错误 → 视图显示"会话已销毁或不存在" |
| 同一连接重复 Attach 同一 sid | 幂等：直接返回 `'a'`（不产生第二次桥接） |
| 视图关闭（✕/弹窗关闭） | 发送 `'D'`，服务端释放通道；会话保留在服务端 |
| 页签销毁 | REST DELETE（既有），服务端向当前通道发 `'E' 0x02`，视图移除 |
| payload > 65535 | 帧编码层拒绝（输入/输出按 32KB 分块，不会触发） |

## 7. 测试计划

以下均已落地，见 `internal/api/ws_multiplex_test.go`：

- **单元**：帧编解码（路由头 round-trip、连接级/会话级、长度越界、
  sid 长度、空会话帧）；
  `Session.Attach` 抢占（旧通道被关、owner 校验、状态不串）。
- **集成（httptest + coder/websocket）**：
  - 单 WS 连接并发附着 2~3 个会话，输入输出独立回显、互不串台；
  - 第二个 WS 连接 Attach 同一会话 → 第一个连接收到 `'E' preempted`
    且状态回 IDLE，新连接收到 `'a'`；
  - WS 关闭 → 所有已附着会话回 IDLE；
  - Attach 已销毁/不存在的会话 → `'b'`；
  - 同一连接重复 Attach 同一 sid → 幂等，仍在用的桥接不受影响；
  - Detach → 该会话回 IDLE，同连接上的其他会话不受影响；
  - 连接级 Ping → Pong（会话级 Ping 也仍然可用）。
- **跨语言契约**：`TestGoDecodesWhatTheBrowserEncodes` 用 TypeScript
  编码器（`session-channel.ts` 经 tsc 编译后）实际产出的字节作为十六进制
  夹具，由 Go 解码器验证——防止两端悄悄漂移。
- **前端**：`vue-tsc --noEmit` + `vite build`；另外用编译出的前端编解码器
  在 Node 里驱动一条真实连接跑通「附着 → ping → 双向输入输出 → detach →
  抢占」全流程。人工验证项：页签切换（v-show 常驻）、延迟显示、
  抢占/断线弹窗。

## 8. 分步实施

1. `internal/terminal/protocol.go`：路由帧编解码 + 新类型常量；
2. `internal/session/session.go`：抢占（`attachEpoch` owner 机制，
   既有实现，本步无需改动）；
3. `internal/api/ws_handler.go`：单连接路由循环 + virtualConn，并按
   `?session_id=` 与否分派新旧协议；
4. 服务端测试（§7）；
5. 前端：`multiplexer.ts` + `session-channel.ts`，`ws.ts` /
   `TerminalPane` 适配；
6. 文档与冒烟验证。

以上 1~6 均已完成；`internal/session/session.go` 如预期未改动。

## 9. 过渡：双协议并存与后续清理

初次实现没有按 §4 初稿移除旧端点，而是让两种协议共用 `/ws`：

| 请求 | 路径 |
|---|---|
| `/ws?session_id=xxx&token=…` | `handleWSSingle`：旧的单会话一条连接 |
| `/ws?token=…`（无 `session_id`） | `handleWSMultiplexed`：`wsRouter` 多路复用 |

理由：多路复用一旦有 bug，终端就是**全部**连不上，而旧路径是现成的退路；
鉴权（令牌 + ws-origin）是两者共用的同一道门，保留旧路径不引入新的攻击面。

前端已随本次改造重建（`apps/web/dist` → `internal/api/static`），因此新
路径是默认路径；旧路径只为「未重建的旧浏览器产物」和排查问题留存。

**后续清理**：确认新路径稳定后，删掉 `handleWSSingle` 与 `wsConn`，
`handleWS` 只保留多路复用分支，并同步更新本文档 §4 与 ADR 相关表述。
删除前需确认没有别的消费者（当前只有前端和 `internal/api` 的测试）。