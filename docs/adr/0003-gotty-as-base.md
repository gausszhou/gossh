# 底座整体搬迁自 gotty(同 MIT),在会话与终端层替换为 SSH 语义

本仓库从 gausszhou/gotty 整体搬迁架构:分层 `internal/api → internal/session → internal/terminal`,WebSocket 二进制帧协议(webtty),浏览器端页签 + localStorage 会话清单模型,以及会话生命周期语义(幂等创建、抢占、空闲淘汰、重放尾屏)。裁剪了与本项目无关的 capture(捕获引擎的浏览器/PNG 渲染与人像渲染保留其 VT 仿真器作为会话镜像)、update(自更新)与 browser(e2e 浏览器驱动)。

替换的核心:
- `internal/terminal` 从"本地命令 PTY(creack/pty)"改为 `internal/sshtty`:通过 `golang.org/x/crypto/ssh` 拨号跳板链,远端请求 PTY,`session.Terminal` 接口不变。
- 会话记录从 `command/args` 变为 `ConnectSpec{host_id,...}`(主机清单引用),复活语义等价(按 id 重连)。

理由:
- 协议与生命周期语义已被生产验证并有完整测试(本仓库保留了 manager/session 的核心测试,会话 id 客户端生成的机制见 ADR 0001),整体重写这些底层是把验证过的代码重造一遍。
- 双方同为 MIT(本仓库整体 MIT,gotty 版权声明保留于 LICENSE),法律上无摩擦。
- 裁剪面是"能力"而非"架构":删掉的能力(capture/update/browser)与本产品定位正交,保留的镜像/查询应答让断开存活(Q14 决策:浏览器断开会话存活、vim/htop 等全屏程序不悬挂)成立。

代价:继承一段对 SSH 场景不完美贴合的既有代码(如 terminal.Options 的 CloseSignal/CloseTimeout 语义仍需 ssh 信号映射),以及若干 gotty 时代的注释/命名残留。