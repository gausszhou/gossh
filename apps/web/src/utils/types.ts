// 共享前端类型:跨组件传递的 UI 状态与 API 契约的 TS 描述。
// 与后端 REST 契约一一对应(字段名严格匹配 internal/api/*.go 的 JSON tag)。

// ── API 契约类型(对应后端 JSON 字段) ──

export type SessionState = 'idle' | 'running' | 'destroyed'

export interface ConnectSpec {
    host_id: string
    name: string
    addr?: string
    user?: string
    group?: string
}

// POST /api/sessions 的返回体(StateDescription)。
export interface StateDescription {
    id: string
    state: SessionState
    spec: ConnectSpec
    exited: boolean
    title?: string // 服务端持久化标题(空 = 未设置)
    created_at: string // RFC3339
}

export type CredentialKind = 'default' | 'key' | 'agent' | 'password'

export interface Credential {
    kind: CredentialKind
    key_path?: string // kind === 'key'
}

export interface HostForward {
    kind: string // 'local' | 'remote' | 'dynamic'
    bind: string
    target?: string
}

// GET /api/hosts 的单条记录。
export interface Host {
    id: string
    name: string
    address: string
    port?: number
    user: string
    group?: string
    credential: Credential
    forwards?: HostForward[]
    created_at?: number
    updated_at?: number
}



// GET /api/known-hosts 的单条记录。
export interface KnownHost {
    addr: string
    key_type: string
    fingerprint: string
    first_seen: number
}

// SFTP 目录项。
export interface SftpEntry {
    name: string
    path: string
    size: number
    mode: string
    is_dir: boolean
    is_link: boolean
    mod_time: number // unix 秒
}

// 会话级端口转发条目。
export interface ForwardEntry {
    id: string
    kind: 'local' | 'remote' | 'dynamic'
    bind: string
    target: string
}

// ── 前端页签模型 ──

export type TabKind = 'ssh' | 'sftp'

export interface AppTab {
    id: string // 页签唯一 id:ssh → 会话 id;其它 → 前缀 + 序号
    kind: TabKind
    title: string
    // ssh/sftp 会话绑定
    sessionId?: string
    hostId?: string
    hostName?: string
    hostLabel?: string // "user@addr:port" 展示用
    // ssh 专属:存活状态(status 轮询)与 WS 附着状态
    alive?: boolean
    connected?: boolean
    latency?: number // ssh 专属:最近一次 ping 往返(ms)
    command?: string
    createdAt: number
}

// 凭据弹窗提交载荷(会话连接的重试参数)。
export interface CredentialPayload {
    password?: string
    passphrase?: string
    savePassword: boolean
    savePassphrase: boolean
}