import { logger } from './logger'
import { getMultiplexer } from './multiplexer'
import { EVENT_PREEMPTED, EVENT_DESTROYED } from './session-channel'

// 终端收发层:一个会话一条逻辑通道,通道跑在全局共享的那条 WebSocket 上
// (多路复用,见 utils/multiplexer.ts 与 docs/design/ws-multiplex.md)。
// 帧格式与服务端 internal/terminal/protocol.go 一致;路由头由 Multiplexer
// 加解,这里看到的仍是老协议那套 [type byte][payload]。

export const WS_PROTOCOLS = ['webtty']

// 客户端(→服务端):输入 / 终端尺寸
const MSG_INPUT = 0x31 // '1'
const MSG_RESIZE = 0x33 // '3'

// 服务端(→客户端):输出 / 窗口标题 / 偏好 / 重连秒数 / 握手完成
const MSG_OUTPUT = 0x31
const MSG_WINDOW_TITLE = 0x33
const MSG_PREFERENCES = 0x34
const MSG_RECONNECT = 0x35
const MSG_REPLAY_DONE = 0x36 // 历史重放已移除;该帧仍是"输入可上行"握手标记

const decoder = new TextDecoder()

// 被抢占:服务端通过会话事件 'E' 告知,不再用 1013 关闭整条连接 ——
// 一条连接上还有别的会话,不能因为一个被接管就全断。
const PREEMPTED_MESSAGE = 'Session is already attached by another client'
const DESTROYED_MESSAGE = 'Session was destroyed'

// TermHandle:xterm 组件只需暴露这几个能力,其余由本模块直接处理。
export interface TermHandle {
    info(): { columns: number, rows: number }
    write(data: Uint8Array): void
    setWindowTitle(title: string): void
    reset(): void
    deactivate(): void
    onInput(callback: (input: string) => void): void
    onResize(callback: (columns: number, rows: number) => void): void
    // 解析完成事件(返回退订函数):输入上行等待重放解析完再开启。
    onWriteParsed(callback: () => void): (() => void) | undefined
}

export interface WSHooks {
    onConnect?: () => void
    onDisconnect?: (message: string) => void
    onGone?: () => void
    // 实测 RTT(毫秒),由上层展示在会话工具栏;null 表示当前测不到
    // (连接已断或会话已结束),上层据此清掉显示。
    onLatency?: (ms: number | null) => void
    // 渲染就绪:收到服务端握手标记(MSG_REPLAY_DONE)。CaptureView 据此
    // 置 window.__gottyCaptureReady,供无头浏览器(capture browser 引擎)
    // 轮询后截图。
    onReady?: () => void
    // 自动重连前确认会话仍存活;返回 null 则停止重连。
    resolveSession?: () => Promise<string | null>
}

// columns/rows 太小即视为"容器尚未就绪"的探测值(FitAddon 在隐藏
// 容器上会把 rows 钳到 1):发出去会把 PTY 缩成 1 行,画面只剩一行、
// 无法向下。过滤,等真实尺寸(激活后的 fit)再发。
function saneSize(columns: number, rows: number): boolean {
    return columns >= 2 && rows >= 2
}

export interface WSWrapper {
    close(): void
    reconnect(): void
}

// openTerminalWS 在一个会话的逻辑通道上完成收发桥接。
// 返回 { close, reconnect } 供组件在卸载/断开弹窗时调用。
export function openTerminalWS(term: TermHandle, sessionId: string, hooks: WSHooks = {}): WSWrapper {
    let closed = false
    let reconnectSeconds = 0
    // xterm 的 onData/onResize 是累加事件:每次 attach 若重新注册,
    // 重连后一次按键会发送多次输入 → 输入输出重复。只注册一次。
    let inputBound = false
    // 输入上行开关:attach 握手完成前关闭。xterm 会对流中出现的终端
    // 查询(DSR/DECRQM/OSC)自动生成应答并经 onData 上行;若在握手完成前
    // 写回 PTY,等于向并不等待的程序注入陈旧应答。收到服务端
    // MSG_REPLAY_DONE 后仍不立即开启 —— xterm 对重放字节流的解析是
    // 异步的,解析中生成的应答会在开启后才到达;这些陈旧应答写回 PTY
    // 后,前台 shell 会把转义载荷显示成乱码(退出 opencode 后刷新所见
    // 的 "10;rgb:..." "$y" 文本)。等重放解析完成(onWriteParsed)再
    // 开启,REPLAY_GATE_MAX_MS 封顶兜底,避免长时间无法输入。
    let inputEnabled = false
    const REPLAY_GATE_MAX_MS = 2000
    let gateTimer: ReturnType<typeof setTimeout> | null = null
    // 解析完成回调的退订;关闭时清理,防跨连接残留。
    let parsedUnsub: (() => void) | null = null
    // 被抢占后不再自动重连:两个客户端会来回抢占,弹窗死循环。
    let preempted = false

    const clearTimers = () => {
        if (gateTimer) {
            clearTimeout(gateTimer)
            gateTimer = null
        }
        if (parsedUnsub) {
            parsedUnsub()
            parsedUnsub = null
        }
    }

    const mux = getMultiplexer()

    const channel = mux.attach(sessionId, {
        resolve: hooks.resolveSession,

        onOpen: () => {
            logger.info('ws', 'attached session=%s', sessionId)
            hooks.onConnect?.()
        },

        onLatency: (ms) => hooks.onLatency?.(ms),

        onFrame: (type, payload) => {
            logger.debug('ws', '<<< frame 0x%s len=%d (session=%s)', type.toString(16), payload.length, sessionId)
            switch (type) {
                case MSG_OUTPUT:
                    term.write(payload)
                    break
                case MSG_WINDOW_TITLE:
                    term.setWindowTitle(decoder.decode(payload))
                    break
                case MSG_PREFERENCES:
                    break // xterm 构造参数已配置,无需动态应用
                case MSG_RECONNECT: {
                    reconnectSeconds = Number(decoder.decode(payload))
                    // 重连秒数是连接级属性:交给共享连接统一调度。
                    mux.setReconnectSeconds(reconnectSeconds)
                    break
                }
                case MSG_REPLAY_DONE:
                    // 重放字节已全部交给 xterm,但解析是异步的;解析过程中
                    // xterm 对重放里的查询生成自动应答 —— 若此刻开启上行,
                    // 这些陈旧应答会写回 PTY,前台 shell 把它们显示成乱码。
                    // 等 onWriteParsed(重放解析完成)再开启;600ms 兜底防卡。
                    hooks.onReady?.() // 握手已到:渲染就绪(供 capture 截图驱动)
                    if (gateTimer) {
                        clearTimeout(gateTimer)
                        gateTimer = null
                    }
                    parsedUnsub?.()
                    let opened = false
                    const open = () => {
                        if (opened) return
                        opened = true
                        inputEnabled = true
                        parsedUnsub?.()
                        parsedUnsub = null
                    }
                    parsedUnsub = term.onWriteParsed(open) ?? null
                    gateTimer = setTimeout(() => {
                        if (!opened) {
                            inputEnabled = true
                            opened = true
                            parsedUnsub?.()
                            parsedUnsub = null
                        }
                    }, 600)
                    break
            }
        },

        onEvent: (status) => {
            if (status === EVENT_DESTROYED) {
                logger.warn('ws', 'session destroyed (session=%s)', sessionId)
                clearTimers()
                inputEnabled = false
                term.deactivate()
                hooks.onGone?.()
                hooks.onLatency?.(null)
                return
            }
            if (status === EVENT_PREEMPTED) {
                // 别的客户端接管了这个会话:告知用户,且不自动重连。
                logger.warn('ws', 'preempted (session=%s)', sessionId)
                preempted = true
                clearTimers()
                inputEnabled = false
                term.deactivate()
                hooks.onDisconnect?.(PREEMPTED_MESSAGE)
                hooks.onLatency?.(null)
            }
        },

        onClose: (message) => {
            clearTimers()
            inputEnabled = false
            term.deactivate()
            logger.info('ws', 'closed session=%s msg=%s', sessionId, message)
            hooks.onDisconnect?.(message)
            hooks.onLatency?.(null)
        },

        onGone: () => {
            logger.warn('ws', 'session gone (session=%s)', sessionId)
            hooks.onGone?.()
        },

        onFail: (reason) => {
            logger.warn('ws', 'attach rejected (session=%s): %s', sessionId, reason)
            term.deactivate()
            hooks.onGone?.()
        },
    })

    // 回调只绑定一次;闭包引用的是最新 channel,始终发往当前附着。
    if (!inputBound) {
        inputBound = true
        term.onInput((input) => {
            if (inputEnabled && !closed) channel.send(MSG_INPUT, input)
        })
        term.onResize((columns, rows) => {
            if (saneSize(columns, rows) && !closed) {
                channel.send(MSG_RESIZE, JSON.stringify({ columns, rows }))
            }
        })
    }
    // 附着后立即上报一次尺寸,让 PTY 与前端几何一致。
    const { columns, rows } = term.info()
    if (saneSize(columns, rows)) {
        channel.send(MSG_RESIZE, JSON.stringify({ columns, rows }))
    }

    return {
        close() {
            closed = true
            clearTimers()
            channel.close()
        },
        reconnect() {
            if (preempted) {
                // 被抢占后不重连:否则两个客户端互相踢,弹窗来回跳。
                logger.warn('ws', 'skip reconnect after preemption (session=%s)', sessionId)
                return
            }
            clearTimers()
            term.reset()
            inputEnabled = false
            mux.reconnectNow()
        },
    }
}
