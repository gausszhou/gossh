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

// GET /api/hosts 的单条记录(附 via_names 连接链展示)。
export interface Host {
    id: string
    name: string
    address: string
    port?: number
    user: string
    group?: string
    credential: Credential
    via?: string // 跳板机 host id
    forwards?: HostForward[]
    created_at?: number
    updated_at?: number
    via_names?: string[] // 服务端附加:跳板链名称(目标除外)
}

// GET /api/hosts/{id}/parents 返回:跳板 id 数组(最近者优先)。
export type HostParents = string[]

// POST /api/run 的返回体。
export interface RunResult {
    host_id: string
    name: string
    command: string
    output: string
    exit_code: number // -1 = 未正常退出(超时/网络)
    error?: string
    duration_ms: number
    host_key_ok: boolean // false → 本次运行首次信任(TOFU)
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

export type TabKind = 'ssh' | 'sftp' | 'run'

export interface AppTab {
    id: string // 页签唯一 id:ssh → 会话 id;其它 → 前缀 + 序号
    kind: TabKind
    title: string
    // ssh/sftp 绑定会话;run 无
    sessionId?: string
    hostId?: string
    hostName?: string
    hostLabel?: string // "user@addr:port" 展示用
    // ssh 专属:存活状态(status 轮询)与 WS 附着状态
    alive?: boolean
    connected?: boolean
    // run 专属:执行结果(页签打开后不再变)
    run?: RunResult
    command?: string
    createdAt: number
}

// 凭据弹窗提交载荷(POST /api/sessions 或 /api/run 的重试参数)。
export interface CredentialPayload {
    password?: string
    passphrase?: string
    savePassword: boolean
    savePassphrase: boolean
}