# 主机密钥信任采用自有 TOFU 存储,不解析 OpenSSH 的 known_hosts

gossh 维护自己的主机密钥信任库 `~/.gossh/known_hosts`(JSON),采用 Trust On First Use:首连记录指纹,后续指纹不匹配即拒绝连接并告警(由前端提供"清除该指纹"入口后重连)。不读取、不写入 OpenSSH 的 `~/.ssh/known_hosts`。

理由:
- OpenSSH known_hosts 格式包含 Host 别名、哈希主机名(`HashKnownHosts`)、多文件 Include、端口前缀 `[host]:port` 等大量兼容面;解析投入高,且一旦解析偏差会制造"明明 OpenSSH 能连,gssh 却报警"的困惑。
- 自有格式(按规范地址 `host:port` 键控的 JSON)与主机清单模型天然对齐——指纹归属显示、删除、审计都在 GoSSH 的语境里。
- 安全强度等价:TOFU + 变更即拒绝。

代价:与用户既有 OpenSSH 信任库不互通(同一台服务器在 OpenSSH 里已信任,GoSSH 首次连接仍需确认一次)。若未来需要互通,可在导入层增量支持 known_hosts 解析(解析器与信任决策解耦,便于替换)。

备选:严格复用 `~/.ssh/known_hosts`(解析成本高,边界情形难测)、完全不校验(对持有私钥的客户端是致命缺口,已否决)。