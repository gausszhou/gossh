<template>
  <!-- 不拦截右键:保留浏览器原生上下文菜单(复制/检查元素等) -->
  <div ref="terminalEl" class="terminal-container"></div>
</template>

<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount } from 'vue'
import { Terminal as XTerminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import { WebglAddon } from '@xterm/addon-webgl'
import { ImageAddon } from '@xterm/addon-image'
import '@xterm/xterm/css/xterm.css'
import { currentTheme, onThemeChange, type Theme } from '../utils/theme'
import { useXTermClipboard, loadClipboardAddon } from '../utils/clipboard'

const props = defineProps<{
    // 使用 DOM 渲染器而非 WebGL:图形协议图片(image addon)在 DOM
    // 渲染器下以 img 元素渲染,截图/合成最稳(capture 渲染页用)。
    domRenderer?: boolean
}>()

const emit = defineEmits<{
    // 服务端 SetWindowTitle 帧;不再直接写 document.title,
    // 由上层(pane 头部)决定如何展示。
    (e: 'title', title: string): void
}>()

// VSCode 集成终端默认字体(platform monospace)的跨平台栈:
// macOS → Menlo/Monaco,Windows → Consolas,Linux → DejaVu Sans Mono。
const FONT_FAMILY =
    'Menlo, Monaco, Consolas, "DejaVu Sans Mono", "Courier New", monospace'

const terminalEl = ref<HTMLElement>()
let term: XTerminal
let fitAddon: FitAddon
let resizeHandler: () => void
// 观察终端容器自身尺寸:侧栏拖宽/折叠等布局变化不触发 window.resize
let resizeObserver: ResizeObserver | null = null
// 同一帧内的多次触发合并成一次 fit(句柄 0 表示当前没有待执行的帧)
let fitRaf = 0
let unsubscribeTheme: (() => void) | null = null

// 终端内部配色跟随亮/暗主题(与页面 CSS 变量一致)。暗色终端面取
// #0c0c0c(Windows Terminal Campbell 底色),比编辑器面 #1f1f1f 更深。
function terminalTheme(theme: Theme): Record<string, string> {
    if (theme === 'light') {
        return {
            background: '#ffffff',
            foreground: '#3b3b3b',
            cursor: '#005fb8',
            cursorAccent: '#ffffff',
            selectionBackground: '#add6ff',
            selectionForeground: '#000000',
        }
    }
    return {
        background: '#0c0c0c',
        foreground: '#cccccc',
        cursor: '#cccccc',
        cursorAccent: '#0c0c0c',
        selectionBackground: '#264f78',
    }
}

onMounted(() => {
  term = new XTerminal({
    cursorBlink: true,
    // 失焦时仍以实心块显示光标:默认 outline 在 WebGL 渲染器下可能不绘制,
    // 表现就是“光标看不见”;block 无论聚焦与否都稳定渲染。
    cursorInactiveStyle: 'block',
    fontSize: 14,
    fontFamily: FONT_FAMILY,
    theme: terminalTheme(currentTheme()),
  })

  fitAddon = new FitAddon()
  term.loadAddon(fitAddon)
  term.loadAddon(new WebLinksAddon())
  // OSC 52:终端内程序(vim/tmux/ssh)读写浏览器系统剪贴板
  loadClipboardAddon(term)

  // 图形协议图片(kitty / sixel / iTerm2 inline):chafa/img2sixel 等
  // 输出在终端里显示为真实图片(WebGL 渲染器下以 overlay 层覆盖)。
  try {
    term.loadAddon(new ImageAddon())
    document.body.dataset.imageAddon = '1'
  } catch (e) {
    // 环境不支持时静默回退(图片退化为占位文本)
    document.body.dataset.imageAddon = '0'
    console.error('image addon failed to load', e)
  }

  // WebGL 渲染器:GPU 不可用(无显卡/远程桌面/部分 headless)时
  // loadAddon 会抛错,自动回退到 xterm 内置的 DOM 渲染器。
  if (!props.domRenderer) {
    try {
      term.loadAddon(new WebglAddon())
    } catch {
      // 回退 DOM 渲染器即可,无需处理
    }
  }

  term.open(terminalEl.value!)

  // 复制/粘贴快捷键(Ctrl+Shift+C/V、Ctrl+C 选区复制、Ctrl+V 粘贴)
  useXTermClipboard(term)

  // 程序设置的终端标题(OSC 0/2,如 vim 的 "vim - file"):
  // xterm 解析后经 onTitleChange 上报,上层据此更新页签标题
  // (GNOME-Shell 风格:标题由程序自动命名/更新)。
  term.onTitleChange((title) => emit('title', title))

  // xterm.css 的 .terminal 规则自带默认等宽字体;显式覆盖到元素上,
  // 保证 WebGL 与 DOM 两种渲染路径都使用配置的字体栈。
  ;(term.element as HTMLElement).style.fontFamily = FONT_FAMILY

  resizeHandler = () => {
    scheduleFit()
  }

  requestAnimationFrame(() => {
    fit()
    window.addEventListener('resize', resizeHandler)
  })

  // 容器尺寸变化统一走 ResizeObserver:仅监听 window.resize 时,只改变
  // 布局(左侧栏拖拽调宽、折叠/展开、会话工具条高度变化)的尺寸变化收不到
  // 事件,xterm 的 cols/rows 会停在旧值 —— 表现为终端不再随容器重排
  // (右缘留白或文本被裁切)。v-show 隐藏的容器为 0×0,fit() 内部已跳过。
  try {
    resizeObserver = new ResizeObserver(() => scheduleFit())
    resizeObserver.observe(terminalEl.value!)
  } catch {
    // 环境不支持 ResizeObserver:退回上面的 window.resize(仅覆盖窗口缩放)
  }

  // 跟随亮/暗主题,动态切换 xterm 内部的配色(纯渲染层;不向 PTY 同步)
  unsubscribeTheme = onThemeChange((theme) => {
    term.options.theme = terminalTheme(theme)
  })
})

onBeforeUnmount(() => {
  if (resizeHandler) window.removeEventListener('resize', resizeHandler)
  resizeObserver?.disconnect()
  resizeObserver = null
  if (fitRaf) cancelAnimationFrame(fitRaf)
  fitRaf = 0
  unsubscribeTheme?.()
  term?.dispose()
})

// 尺寸变化的唯一入口:窗口缩放与容器自身尺寸变化都汇到这里,同一帧内只
// 执行一次 fit(拖拽侧栏时 pointermove 频率高于渲染帧,RO 也会连续触发;
// 逐次 fit 会向 PTY 连发 resize 帧)。fit 会改变容器内部结构,放进下一帧
// 执行同时避开 "ResizeObserver loop completed with undelivered
// notifications" 告警。
function scheduleFit() {
  if (fitRaf) return
  fitRaf = requestAnimationFrame(() => {
    fitRaf = 0
    fit()
  })
}

// fit 重新适配容器尺寸;v-show 隐藏后重新显示时必须调用(激活 watcher)。
// 隐藏(v-show display:none)或未布局的容器高度为 0:FitAddon 会把 rows
// 钳到 1 并 resize 出"1 行终端",该会话从此只剩一行、光标永远在第一行、
// 无法向下 —— 因此零尺寸时跳过,等可见后再由上层 fit。
//
// 不直接调用 fitAddon.fit():它算列数时会固定预留 14px 给滚动条
// (@xterm/addon-fit 0.11.0:`scrollback !== 0 ? overviewRuler?.width || 14 : 0`,
// 传 0 也会被 `||` 回落到 14;上游 master 已改成 options.scrollbar,尚未发版),
// 于是网格右缘永远空出一条 14px 留白,和左侧 7px 内边距不对称。这里只用它
// 的 proposeDimensions(算但不应用)拿到行数与单元格尺寸,再按容器全宽把
// 这条留白折算成列补回去,一次 resize 到位。
function fit() {
  const el = terminalEl.value
  if (!el || el.clientWidth === 0 || el.clientHeight === 0) return
  const dims = fitAddon?.proposeDimensions()
  if (!dims) return
  const cols = fullWidthCols(el, dims.cols)
  if (cols !== term.cols || dims.rows !== term.rows) {
    term.resize(cols, dims.rows)
  }
}

// fullWidthCols 用容器实际宽度重算列数,把 FitAddon 预留的滚动条宽度
// 还回来。单元格宽度取自**当前已渲染**的网格:.xterm-screen 的宽度除以
// term.cols(WebGL 与 DOM 渲染器都会把 screen 元素设成 cols × cellWidth),
// 因此不依赖 xterm 私有 API。注意不能用 dims.cols 去除 —— 那是 FitAddon
// 按"预留后"宽度算出来的列数,和 screen 的宽度不在同一把尺子上,算出的
// 单元格宽度会偏大,留白就收不回来。测不出时退回 FitAddon 的结果。
function fullWidthCols(el: HTMLElement, fallbackCols: number): number {
  const current = term?.cols ?? 0
  if (current < 2) return fallbackCols
  const screen = term?.element?.querySelector<HTMLElement>('.xterm-screen')
  if (!screen) return fallbackCols
  const width = screen.getBoundingClientRect().width
  if (!(width > 0)) return fallbackCols
  const cellWidth = width / current
  if (!Number.isFinite(cellWidth) || cellWidth <= 0) return fallbackCols
  return Math.max(fallbackCols, Math.floor(el.clientWidth / cellWidth))
}

function info() {
  return { columns: term.cols, rows: term.rows }
}

function write(data: Uint8Array) {
  term?.write(data)
}

function setWindowTitle(title: string) {
  emit('title', title)
}

function setPreferences(_value: object) {
  // no-op: xterm.js v5+ handles config via Terminal constructor options
}

function onInput(callback: (input: string) => void) {
  term?.onData((data) => callback(data))
}

function onResize(callback: (columns: number, rows: number) => void) {
  term?.onResize(({ cols, rows }) => callback(cols, rows))
}

// onWriteParsed 在 xterm 解析完一批写入后触发;返回退订函数。
// ws.ts 用它把"输入上行"的开启推迟到重放字节全部解析完成之后:
// 重放里的终端查询会触发 xterm 自动应答,若在解析完成前就放开上行,
// 这些陈旧应答被写回 PTY,前台 shell 会把转义载荷显示成乱码。
function onWriteParsed(callback: () => void): (() => void) | undefined {
  if (!term) return undefined
  const d = term.onWriteParsed(() => callback())
  return () => d.dispose()
}

function reset() {
  term?.clear()
}

// focus 把键盘焦点交给 xterm 的输入区;激活/创建会话后由上层调用,
// 让用户无需点击终端即可直接输入。
function focus() {
  term?.focus()
}

function deactivate() {
  term?.blur()
}

function close() {
  term?.dispose()
}

defineExpose({
  info,
  write,
  setWindowTitle,
  setPreferences,
  onInput,
  onResize,
  onWriteParsed,
  reset,
  focus,
  deactivate,
  fit,
  close,
})
</script>

<style scoped>
.terminal-container {
    width: 100%;
    height: 100%;
    background: var(--term-bg);
    padding: 0;
    margin: 0;
    overflow: hidden;
}
</style>
