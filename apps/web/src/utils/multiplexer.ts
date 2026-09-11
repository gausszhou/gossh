import { logger } from './logger'
import { getToken } from './api'
import {
    SessionChannel,
    type ChannelHandlers,
    type ChannelWire,
    encodeRouted,
    encodeConnLevel,
    decodeRouted,
    MSG_ATTACH,
    MSG_DETACH,
    MSG_ATTACH_OK,
    MSG_ATTACH_FAIL,
    MSG_SESSION_EVENT,
} from './session-channel'

// 全局唯一的一条 WebSocket:所有会话的终端都跑在它上面,靠帧头的
// session_id 路由。浏览器同域 ~6 条 WS 的上限因此不再限制能同时打开
// 多少个常驻会话(v-show 视图)。见 docs/design/ws-multiplex.md。

const WS_PROTOCOLS = ['webtty']

// Ping 是连接级消息:一条 socket 一个心跳,测出的 RTT 广播给所有通道
// (延迟本来就是这条连接的属性,与具体会话无关)。
const MSG_PING = 0x32 // '2'
const MSG_PONG = 0x32 // '2'
const PING_INTERVAL_MS = 2000

const EMPTY = new Uint8Array(0)
const decoder = new TextDecoder()

// 抢占现在走会话事件 'E',不再用 1013 关闭整条连接 —— 多路复用下一条
// 连接上还有别的会话,不能因为一个会话被接管就全部断开。
function disconnectMessage(code: number, text: string): string {
    if (text) return text
    switch (code) {
        case 1006:
            return 'Network connection lost'
        case 1011:
            return 'Server error'
        default:
            return 'Connection closed'
    }
}

interface Attachment {
    channel: SessionChannel
    handlers: ChannelHandlers
}

export class Multiplexer implements ChannelWire {
    private ws: WebSocket | null = null
    private attachments = new Map<string, Attachment>()
    private pingTimer: ReturnType<typeof setInterval> | null = null
    private reconnectTimer: ReturnType<typeof setTimeout> | null = null
    private pendingPingAt: number | null = null
    private reconnectSeconds = 0

    // ---- 对外 API ----

    /** 附着一个会话,返回它的逻辑通道。重复 attach 同一 sid 会复用同一条通道。 */
    attach(sid: string, handlers: ChannelHandlers = {}): SessionChannel {
        const existing = this.attachments.get(sid)
        if (existing) {
            existing.handlers = handlers
            existing.channel._reopen()
            logger.debug('mux', 'reusing channel for session=%s', sid)
            return existing.channel
        }

        const channel = new SessionChannel(sid, this, handlers)
        this.attachments.set(sid, { channel, handlers })
        logger.info('mux', 'attach session=%s (%d on this connection)', sid, this.attachments.size)

        if (this.ws && this.ws.readyState === WebSocket.OPEN) {
            this.sendRouted(sid, MSG_ATTACH)
        } else {
            this.open() // onopen 会把表里的会话逐一附着
        }
        return channel
    }

    /** ChannelWire:通道发一条会话级帧。 */
    send(sid: string, type: number, payload: Uint8Array = EMPTY): void {
        this.sendRouted(sid, type, payload)
    }

    /** ChannelWire:通道主动分离。 */
    detach(sid: string): void {
        const att = this.attachments.get(sid)
        if (!att) return
        this.attachments.delete(sid)
        if (this.ws && this.ws.readyState === WebSocket.OPEN) {
            this.ws.send(encodeRouted(sid, MSG_DETACH, EMPTY))
        }
        logger.info('mux', 'detach session=%s (%d left)', sid, this.attachments.size)
        this.disposeIfIdle()
    }

    /** 服务端下发的重连秒数(SetReconnect 帧),连接级共享。 */
    setReconnectSeconds(seconds: number): void {
        if (seconds > 0) this.reconnectSeconds = seconds
    }

    /** 立刻重开一条连接(断开弹窗里手动点"重新连接")。 */
    reconnectNow(): void {
        if (this.reconnectTimer) {
            clearTimeout(this.reconnectTimer)
            this.reconnectTimer = null
        }
        for (const [, att] of this.attachments) att.channel._reopen()
        this.dropSocket()
        this.open()
    }

    /** 当前是否连着(供 UI 判断)。 */
    get connected(): boolean {
        return this.ws !== null && this.ws.readyState === WebSocket.OPEN
    }

    // ---- 连接管理 ----

    private open(): void {
        if (this.ws) return
        const scheme = window.location.protocol === 'https:' ? 'wss://' : 'ws://'
        const token = getToken()
        // 关键:多路复用端点不带 session_id —— 带了会走旧的单会话协议。
        const tokenQuery = token ? `?token=${encodeURIComponent(token)}` : ''
        const url = `${scheme}${window.location.host}/ws${tokenQuery}`

        const ws = new WebSocket(url, WS_PROTOCOLS)
        ws.binaryType = 'arraybuffer'
        this.ws = ws

        ws.onopen = () => {
            logger.info('mux', 'connected, attaching %d session(s)', this.attachments.size)
            this.startPing()
            for (const sid of this.attachments.keys()) {
                this.sendRouted(sid, MSG_ATTACH)
            }
        }

        ws.onmessage = (ev) => {
            if (!(ev.data instanceof ArrayBuffer)) {
                logger.warn('mux', 'skip non-binary message (%s)', typeof ev.data)
                return
            }
            this.dispatch(new Uint8Array(ev.data))
        }

        ws.onclose = (ev) => {
            this.stopPing()
            this.ws = null
            const message = disconnectMessage(ev.code, ev.reason)
            logger.info('mux', 'closed code=%d msg=%s', ev.code, message)
            for (const [, att] of this.attachments) att.channel._deliverClose(message)
            if (this.reconnectSeconds > 0 && this.attachments.size > 0) {
                this.scheduleReconnect()
            }
        }
    }

    private dispatch(data: Uint8Array): void {
        const f = decodeRouted(data)
        if (!f) {
            logger.warn('mux', 'drop malformed routed frame (%d bytes)', data.length)
            return
        }

        if (f.connLevel) {
            if (f.type === MSG_PONG) this.onPong()
            return
        }

        const att = this.attachments.get(f.sid)
        if (!att) {
            logger.debug('mux', 'frame for unknown session %s, dropped', f.sid)
            return
        }

        switch (f.type) {
            case MSG_ATTACH_OK:
                att.channel._deliverOpen()
                break
            case MSG_ATTACH_FAIL:
                att.channel._deliverFail(decoder.decode(f.payload))
                break
            case MSG_SESSION_EVENT:
                att.channel._deliverEvent(f.payload.length > 0 ? f.payload[0] : 0)
                break
            default:
                att.channel._deliverFrame(f.type, f.payload)
        }
    }

    private sendRouted(sid: string, type: number, payload: Uint8Array = EMPTY): void {
        const ws = this.ws
        if (!ws || ws.readyState !== WebSocket.OPEN) {
            logger.warn('mux', 'drop frame 0x%s for %s: socket not open', type.toString(16), sid)
            return
        }
        ws.send(encodeRouted(sid, type, payload))
    }

    // ---- 心跳与延迟 ----

    private startPing(): void {
        this.stopPing()
        this.pendingPingAt = performance.now()
        this.ws?.send(encodeConnLevel(MSG_PING))
        this.pingTimer = setInterval(() => {
            this.pendingPingAt = performance.now()
            if (this.ws && this.ws.readyState === WebSocket.OPEN) {
                this.ws.send(encodeConnLevel(MSG_PING))
            }
        }, PING_INTERVAL_MS)
    }

    private stopPing(): void {
        if (this.pingTimer) {
            clearInterval(this.pingTimer)
            this.pingTimer = null
        }
        this.pendingPingAt = null
    }

    private onPong(): void {
        if (this.pendingPingAt === null) return
        const rtt = Math.round(performance.now() - this.pendingPingAt)
        this.pendingPingAt = null
        for (const [, att] of this.attachments) att.channel._deliverLatency(rtt)
    }

    // ---- 重连 ----

    private scheduleReconnect(): void {
        if (this.reconnectTimer) return
        this.reconnectTimer = setTimeout(() => {
            this.reconnectTimer = null
            void this.reconnectAttachments()
        }, this.reconnectSeconds * 1000)
    }

    private async reconnectAttachments(): Promise<void> {
        // 先逐个确认会话还活着;已经消失的(销毁/空闲淘汰)不再重连。
        for (const [sid, att] of [...this.attachments]) {
            if (!att.handlers.resolve) continue
            const id = await att.handlers.resolve()
            if (id === null || id === undefined) {
                this.attachments.delete(sid)
                att.channel._deliverGone()
            }
        }
        if (this.attachments.size === 0) {
            logger.warn('mux', 'no live sessions left, stop reconnecting')
            return
        }
        for (const [, att] of this.attachments) att.channel._reopen()
        this.dropSocket()
        this.open()
    }

    /** 摘掉 socket 的回调再关,避免 close 事件又触发一轮重连。 */
    private dropSocket(): void {
        const ws = this.ws
        if (!ws) return
        this.ws = null
        ws.onopen = null
        ws.onmessage = null
        ws.onclose = null
        try {
            ws.close()
        } catch {
            /* 已关闭的忽略 */
        }
    }

    /** 没有会话再占用这条连接时收掉它,避免留一条空转的 WS。 */
    private disposeIfIdle(): void {
        if (this.attachments.size > 0) return
        this.stopPing()
        if (this.reconnectTimer) {
            clearTimeout(this.reconnectTimer)
            this.reconnectTimer = null
        }
        this.reconnectSeconds = 0
        this.dropSocket()
    }
}

let global: Multiplexer | null = null

/** 整页共享的那一个 Multiplexer。 */
export function getMultiplexer(): Multiplexer {
    if (!global) global = new Multiplexer()
    return global
}
