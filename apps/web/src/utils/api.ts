// 与 gossh REST API 通信的薄封装。
// 访问令牌:从页面 URL ?token= 读取并存入 sessionStorage;
// 之后所有 /api/* 请求携带 X-Gossh-Token 头(服务端同时接受
// Authorization: Bearer 与 ?token=,见 internal/api/auth.go)。
// 会话页签由客户端 localStorage 清单(utils/manifest.ts)驱动,
// 服务端只按 id 提供:创建(幂等/复活)、详情、状态批量查询、销毁。
import { logger } from './logger'
import type {
    Host, HostParents, KnownHost, SftpEntry, StateDescription, ForwardEntry,
} from './types'

const TOKEN_KEY = 'gossh.token'

// getToken 返回访问令牌:优先 sessionStorage,其次从 URL query 解析并缓存。
export function getToken(): string {
    try {
        const cached = sessionStorage.getItem(TOKEN_KEY)
        if (cached) return cached
    } catch {
        // sessionStorage 不可用时直接解析 URL
    }
    let token = ''
    try {
        const m = /[?&]token=([^&]+)/.exec(window.location.search)
        if (m) token = decodeURIComponent(m[1])
    } catch {
        // 忽略解析失败
    }
    if (token) {
        try {
            sessionStorage.setItem(TOKEN_KEY, token)
        } catch {
            // 忽略持久化失败
        }
    }
    return token
}

export class APIError extends Error {
    status: number

    constructor(status: number, message: string) {
        super(message)
        this.status = status
    }
}

// authHeaders 附加令牌头(不覆盖调用方显式指定的同名头)。
function authHeaders(extra?: HeadersInit): HeadersInit {
    const headers: Record<string, string> = {}
    const token = getToken()
    if (token) headers['X-Gossh-Token'] = token
    if (extra) {
        for (const [k, v] of Object.entries(extra)) {
            if (v !== undefined && v !== null) headers[k] = String(v)
        }
    }
    return headers
}

async function errorMessage(res: Response, fallback: string): Promise<string> {
    try {
        const body = await res.json()
        if (body && typeof body.error === 'string') return body.error
    } catch {
        // 非 JSON 错误体,用默认消息
    }
    return fallback
}

export async function fetchJSON<T>(url: string, init?: RequestInit): Promise<T> {
    const method = init?.method || 'GET'
    logger.debug('api', '%s %s', method, url)
    const res = await fetch(url, { ...init, headers: authHeaders(init?.headers) })
    if (!res.ok) {
        const message = await errorMessage(res, `request to ${url} failed with status ${res.status}`)
        logger.warn('api', '%s %s -> %d: %s', method, url, res.status, message)
        throw new APIError(res.status, message)
    }
    return res.json() as Promise<T>
}

// fetchRaw 用于二进制流(下载)或原始 body(上传)。
async function fetchRaw(url: string, init?: RequestInit): Promise<Response> {
    const res = await fetch(url, { ...init, headers: authHeaders(init?.headers) })
    if (!res.ok) {
        const message = await errorMessage(res, `request to ${url} failed with status ${res.status}`)
        throw new APIError(res.status, message)
    }
    return res
}

// ── 错误分类 ──

// isCredentialError 判断会话/运行创建失败是否与凭据有关(需要弹凭据框):
// 后端对无效请求返回 400;认证失败(密码/口令/keyring)经创建链路返回
// 500 且错误信息含 authentication/password 等字样。兜底:任何 400 都视为
// 需要用户提供凭据(符合"任何 400 都弹凭据模态框"的约定)。
export function isCredentialError(err: unknown): boolean {
    if (err instanceof APIError) {
        if (err.status === 400) return true
        if (err.status === 500 && /password|passphrase|authenticate|authentication|permission denied/i.test(err.message)) {
            return true
        }
    }
    return false
}

// ── 会话(session) ──

export interface SessionCreateOptions {
    id?: string // 前端生成的 16 位 base36 会话 id
    host_id: string
    password?: string
    passphrase?: string
    save_password?: boolean
    save_passphrase?: boolean
}

// createSession 新建会话(幂等/复活语义);返回 201(新建)或 200(已存在)。
export async function createSession(opts: SessionCreateOptions): Promise<StateDescription> {
    const body: Record<string, unknown> = { host_id: opts.host_id }
    if (opts.id) body.id = opts.id
    if (opts.password !== undefined && opts.password !== '') body.password = opts.password
    if (opts.passphrase !== undefined && opts.passphrase !== '') body.passphrase = opts.passphrase
    if (opts.save_password) body.save_password = true
    if (opts.save_passphrase) body.save_passphrase = true
    return fetchJSON<StateDescription>('/api/sessions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
    })
}

// checkSessions 批量查询清单中 id 的存活状态(status 轮询):
// 返回存活的会话,未返回的 id 即服务端已无存活记录。
export async function checkSessions(ids: string[]): Promise<StateDescription[]> {
    if (ids.length === 0) return []
    const data = await fetchJSON<{ sessions: Record<string, StateDescription> }>(
        '/api/sessions/status',
        {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ ids }),
        },
    )
    return data.sessions ? Object.values(data.sessions) : []
}

// getSession 获取单个会话详情。
export async function getSession(id: string): Promise<StateDescription | null> {
    try {
        return await fetchJSON<StateDescription>(`/api/sessions/${encodeURIComponent(id)}`)
    } catch {
        return null
    }
}

export async function destroySession(id: string): Promise<void> {
    try {
        await fetchRaw(`/api/sessions/${encodeURIComponent(id)}`, { method: 'DELETE' })
    } catch {
        // 会话可能已经不存在
    }
}

// updateSessionTitle 持久化会话标题(PUT /api/sessions/{id}/title)。
export async function updateSessionTitle(id: string, title: string): Promise<void> {
    try {
        await fetchJSON<{ title: string }>(`/api/sessions/${encodeURIComponent(id)}/title`, {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ title }),
        })
    } catch {
        // 标题持久化失败不阻塞界面
    }
}

// ── 主机清单(host inventory) ──

export async function listHosts(): Promise<Host[]> {
    return fetchJSON<Host[]>('/api/hosts')
}

// createHost 新建主机;id 由服务端生成。
export async function createHost(host: Host): Promise<Host> {
    return fetchJSON<Host>('/api/hosts', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(host),
    })
}

// updateHost 整字段替换 PUT /api/hosts/{id}。
export async function updateHost(host: Host): Promise<Host> {
    return fetchJSON<Host>(`/api/hosts/${encodeURIComponent(host.id)}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(host),
    })
}

export async function deleteHost(id: string): Promise<void> {
    await fetchRaw(`/api/hosts/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

// hostParents 取得跳板 id 链(最近者优先)。
export async function hostParents(id: string): Promise<HostParents> {
    return fetchJSON<HostParents>(`/api/hosts/${encodeURIComponent(id)}/parents`)
}

// ── 已知主机密钥(TOFU trust store) ──

export async function listKnownHosts(): Promise<KnownHost[]> {
    return fetchJSON<KnownHost[]>('/api/known-hosts')
}

export async function forgetKnownHost(addr: string): Promise<void> {
    await fetchRaw(`/api/known-hosts/${encodeURIComponent(addr)}`, { method: 'DELETE' })
}

// ── 系统钥匙串秘密 ──

export interface SecretInput {
    kind: 'password' | 'passphrase'
    addr?: string
    user?: string
    key_path?: string
    secret: string
}

export async function setSecret(input: SecretInput): Promise<void> {
    await fetchJSON('/api/secrets', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(input),
    })
}

export async function deleteSecret(input: Omit<SecretInput, 'secret'>): Promise<void> {
    const q = new URLSearchParams()
    if (input.kind === 'password') {
        q.set('kind', 'password')
        if (input.addr) q.set('addr', input.addr)
        if (input.user) q.set('user', input.user)
    } else {
        q.set('kind', 'passphrase')
        if (input.key_path) q.set('key_path', input.key_path)
    }
    await fetchRaw(`/api/secrets?${q.toString()}`, { method: 'DELETE' })
}

// ── SFTP(绑定会话) ──

export async function sftpList(sessionId: string, path: string): Promise<SftpEntry[]> {
    const q = new URLSearchParams({ path: path || '.' })
    return fetchJSON<SftpEntry[]>(`/api/sessions/${encodeURIComponent(sessionId)}/sftp/ls?${q.toString()}`)
}

export async function sftpStat(sessionId: string, path: string): Promise<SftpEntry> {
    const q = new URLSearchParams({ path })
    return fetchJSON<SftpEntry>(`/api/sessions/${encodeURIComponent(sessionId)}/sftp/stat?${q.toString()}`)
}

export async function sftpMkdir(sessionId: string, path: string): Promise<void> {
    await fetchJSON(`/api/sessions/${encodeURIComponent(sessionId)}/sftp/mkdir`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path }),
    })
}

export async function sftpRename(sessionId: string, from: string, to: string): Promise<void> {
    await fetchJSON(`/api/sessions/${encodeURIComponent(sessionId)}/sftp/rename`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ from, to }),
    })
}

export async function sftpRemove(sessionId: string, path: string): Promise<void> {
    await fetchJSON(`/api/sessions/${encodeURIComponent(sessionId)}/sftp/remove`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path }),
    })
}

// sftpDownload 以 Blob 拉取远程文件(由调用方触发浏览器保存)。
export async function sftpDownload(sessionId: string, path: string): Promise<Blob> {
    const q = new URLSearchParams({ path })
    const res = await fetchRaw(`/api/sessions/${encodeURIComponent(sessionId)}/sftp/download?${q.toString()}`)
    return res.blob()
}

// sftpUpload 将文件内容写入远程路径(原始 body 上传)。
export async function sftpUpload(sessionId: string, path: string, body: Blob): Promise<{ written: number }> {
    const q = new URLSearchParams({ path })
    return fetchJSON<{ written: number }>(`/api/sessions/${encodeURIComponent(sessionId)}/sftp/upload?${q.toString()}`, {
        method: 'POST',
        body,
    })
}

// ── 端口转发(绑定会话) ──

export async function listForwards(sessionId: string): Promise<ForwardEntry[]> {
    return fetchJSON<ForwardEntry[]>(`/api/sessions/${encodeURIComponent(sessionId)}/forwards`)
}

export interface ForwardInput {
    kind: 'local' | 'remote' | 'dynamic'
    bind: string
    target?: string
}

export async function addForward(sessionId: string, input: ForwardInput): Promise<ForwardEntry> {
    return fetchJSON<ForwardEntry>(`/api/sessions/${encodeURIComponent(sessionId)}/forwards`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(input),
    })
}

export async function deleteForward(sessionId: string, forwardId: string): Promise<void> {
    await fetchRaw(`/api/sessions/${encodeURIComponent(sessionId)}/forwards/${encodeURIComponent(forwardId)}`, {
        method: 'DELETE',
    })
}

// ── 部署级页面标题 ──

export async function getPageTitle(): Promise<string> {
    const data = await fetchJSON<{ title: string }>('/api/title')
    return data.title || ''
}

export async function setPageTitle(title: string): Promise<string> {
    const data = await fetchJSON<{ title: string }>('/api/title', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ title }),
    })
    return data.title || ''
}