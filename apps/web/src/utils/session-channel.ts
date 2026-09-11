import { logger } from './logger'

// 多路复用协议的帧编解码与逻辑通道。
// 帧格式(与服务端 internal/terminal/protocol.go 一致):
//
//	[session_id (16B)] [type (1B)] [len (2B BE)] [payload (len B)]
//
// session_id 全 0x00 表示"连接级"消息(Ping/Pong),否则是会话级。
// 会话级 payload 仍是老协议那一套 [type][payload] 帧,只是外面套了路由头,
// 因此 xterm 桥接逻辑(utils/ws.ts)一行都不用改。

export const SESSION_ID_LEN = 16
export const ROUTED_HEADER_LEN = SESSION_ID_LEN + 3
const MAX_PAYLOAD = 0xffff

// 连接级:16 个 0x00
const CONN_LEVEL_SID = '\0'.repeat(SESSION_ID_LEN)

// 多路复用控制消息
export const MSG_ATTACH = 0x41 // 'A' C→S 附着
export const MSG_DETACH = 0x44 // 'D' C→S 分离
export const MSG_ATTACH_OK = 0x61 // 'a' S→C 附着成功
export const MSG_ATTACH_FAIL = 0x62 // 'b' S→C 附着失败,payload 是原因
export const MSG_SESSION_EVENT = 0x45 // 'E' S→C 会话事件,payload 1 字节

// 会话事件状态
export const EVENT_PREEMPTED = 0x01 // 被其他客户端接管
export const EVENT_DESTROYED = 0x02 // 会话已销毁

export interface RoutedFrame {
    /** 会话 id;连接级帧为空串。 */
    sid: string
    /** 是否为连接级帧。 */
    connLevel: boolean
    type: number
    payload: Uint8Array
}

function writeSid(frame: Uint8Array, sid: string): void {
    for (let i = 0; i < SESSION_ID_LEN; i++) {
        frame[i] = i < sid.length ? sid.charCodeAt(i) & 0xff : 0
    }
}

/** 编码一条会话级路由帧。 */
export function encodeRouted(sid: string, type: number, payload: Uint8Array = EMPTY): Uint8Array {
    const frame = new Uint8Array(ROUTED_HEADER_LEN + payload.length)
    writeSid(frame, sid)
    frame[SESSION_ID_LEN] = type
    frame[SESSION_ID_LEN + 1] = (payload.length >> 8) & 0xff
    frame[SESSION_ID_LEN + 2] = payload.length & 0xff
    frame.set(payload, ROUTED_HEADER_LEN)
    return frame
}

const EMPTY = new Uint8Array(0)

/** 编码一条连接级路由帧(Ping)。 */
export function encodeConnLevel(type: number, payload: Uint8Array = EMPTY): Uint8Array {
    const frame = new Uint8Array(ROUTED_HEADER_LEN + payload.length)
    writeSid(frame, CONN_LEVEL_SID)
    frame[SESSION_ID_LEN] = type
    frame[SESSION_ID_LEN + 1] = (payload.length >> 8) & 0xff
    frame[SESSION_ID_LEN + 2] = payload.length & 0xff
    frame.set(payload, ROUTED_HEADER_LEN)
    return frame
}

/** 解码一条路由帧;格式不合法时返回 null(调用方丢弃即可)。 */
export function decodeRouted(data: Uint8Array): RoutedFrame | null {
    if (data.length < ROUTED_HEADER_LEN) return null
    const raw = new Array(SESSION_ID_LEN)
    for (let i = 0; i < SESSION_ID_LEN; i++) raw[i] = data[i]
    const sid = String.fromCharCode(...raw)
    const connLevel = sid === CONN_LEVEL_SID
    const type = data[SESSION_ID_LEN]
    const declared = (data[SESSION_ID_LEN + 1] << 8) | data[SESSION_ID_LEN + 2]
    const payload = data.subarray(ROUTED_HEADER_LEN)
    if (declared !== payload.length || declared > MAX_PAYLOAD) return null
    return { sid: connLevel ? '' : sid, connLevel, type, payload }
}

// ---------------------------------------------------------------------------
// 逻辑通道
// ---------------------------------------------------------------------------

/**
 * 通道的写出口。由 Multiplexer 实现:通道只管"我要发什么",
 * 往哪条 socket 上发、要不要先重连,都是 Multiplexer 的事。
 */
export interface ChannelWire {
    send(sid: string, type: number, payload: Uint8Array): void
    /** 主动分离:通知服务端释放通道,会话本身保留。 */
    detach(sid: string): void
}

export interface ChannelHandlers {
    /** 附着成功(收到 'a'),此后才开始推输出。 */
    onOpen?: () => void
    /** 附着失败(收到 'b'),payload 是服务端给的原因。 */
    onFail?: (reason: string) => void
    /** 会话级数据帧。 */
    onFrame?: (type: number, payload: Uint8Array) => void
    /** 会话事件:'E' 帧的 status。 */
    onEvent?: (status: number) => void
    /** 通道关闭(连接断开或本地 close)。 */
    onClose?: (reason: string) => void
    /** 重连前确认会话仍存活;返回 null 表示已消失。 */
    resolve?: () => Promise<string | null>
    /** 重连前发现会话已不存在(被销毁/淘汰)。 */
    onGone?: () => void
    /** 连接级 RTT(毫秒):延迟是这条共享连接的属性,所有通道同一个值。 */
    onLatency?: (ms: number) => void
}

/**
 * SessionChannel 是一次 attach 的逻辑通道,等价于老协议里的一条 WebSocket:
 * 读写都带上自己的 sid,由 Multiplexer 在同一条 socket 上复用。
 */
export class SessionChannel {
    private opened = false
    private closed = false

    constructor(
        readonly sid: string,
        private wire: ChannelWire,
        private handlers: ChannelHandlers = {},
    ) {}

    /** 发送一条会话级帧(type + payload)。 */
    send(type: number, payload?: Uint8Array | string): void {
        if (this.closed) return
        const body =
            payload === undefined
                ? EMPTY
                : typeof payload === 'string'
                  ? new TextEncoder().encode(payload)
                  : payload
        this.wire.send(this.sid, type, body)
    }

    /** 主动分离并丢弃后续回调。 */
    close(): void {
        if (this.closed) return
        this.closed = true
        this.wire.detach(this.sid)
    }

    // ---- 以下由 Multiplexer 调用 ----

    _deliverOpen(): void {
        if (this.opened || this.closed) return
        this.opened = true
        this.handlers.onOpen?.()
    }

    _deliverFail(reason: string): void {
        if (this.closed) return
        logger.warn('channel', 'attach failed (session=%s): %s', this.sid, reason)
        this.handlers.onFail?.(reason)
    }

    _deliverFrame(type: number, payload: Uint8Array): void {
        if (this.closed) return
        this.handlers.onFrame?.(type, payload)
    }

    _deliverEvent(status: number): void {
        if (this.closed) return
        this.handlers.onEvent?.(status)
    }

    _deliverGone(): void {
        if (this.closed) return
        this.closed = true
        this.handlers.onGone?.()
    }

    _deliverLatency(ms: number): void {
        if (this.closed) return
        this.handlers.onLatency?.(ms)
    }

    /** Multiplexer 在连接断开或自己移除通道时调用;reason 供上层弹窗用。 */
    _deliverClose(reason: string): void {
        if (this.closed) return
        this.closed = true
        this.handlers.onClose?.(reason)
    }

    /** 重连后复用:清掉"已关闭"标记,让这条通道继续收帧。 */
    _reopen(): void {
        this.closed = false
    }

    get isClosed(): boolean {
        return this.closed
    }
}
