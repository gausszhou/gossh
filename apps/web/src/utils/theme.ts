// 亮/暗主题:通过 <html data-theme="dark|light"> 切换 CSS 变量,
// 偏好持久化在 localStorage("gotty.theme"),并广播事件给 xterm(动态主题)。
//
// 两级概念:
//   ThemePreference —— 用户的选择:light | dark | system(默认 system)。
//   Theme           —— 实际生效的外观:light | dark(system 由系统偏好解析而来)。
// 只有解析后的 Theme 会写进 data-theme 并广播,组件因此只需处理亮/暗两种。
//
// 注意:THEME_KEY 的读取逻辑在 apps/web/index.html 的内联脚本里有一份副本
// (首帧防闪烁,必须同步执行),改动键名或取值时两处要一起改。
import { logger } from './logger'

export type Theme = 'dark' | 'light'
export type ThemePreference = Theme | 'system'

const THEME_KEY = 'gotty.theme'
const THEME_EVENT = 'gotty:theme'
const SYSTEM_DARK_QUERY = '(prefers-color-scheme: dark)'

// 无持久化偏好(首次访问)时的默认值。
const DEFAULT_PREFERENCE: ThemePreference = 'system'

function isPreference(v: string | null): v is ThemePreference {
    return v === 'dark' || v === 'light' || v === 'system'
}

// currentPreference 读取用户选择;未设置或取值非法(旧版本数据)时回退默认值。
export function currentPreference(): ThemePreference {
    try {
        const v = localStorage.getItem(THEME_KEY)
        return isPreference(v) ? v : DEFAULT_PREFERENCE
    } catch {
        return DEFAULT_PREFERENCE
    }
}

// systemDarkQueryMatches 系统当前是否为暗色;matchMedia 不可用时按暗色处理
// (与 CSS 里 :root 的暗色默认值一致,保证不会出现"亮色变量配暗色样式")。
export function systemDarkQueryMatches(): boolean {
    try {
        return window.matchMedia(SYSTEM_DARK_QUERY).matches
    } catch {
        return true
    }
}

// resolveTheme 把用户选择解析成实际生效的亮/暗外观。
// 需要"跟随系统变化"的调用方应把系统快照放进响应式状态(见 App.vue),
// 单次解析用它即可。
export function resolveTheme(pref: ThemePreference): Theme {
    if (pref === 'system') return systemDarkQueryMatches() ? 'dark' : 'light'
    return pref
}

// currentTheme 返回当前实际生效的主题(已解析 system)。
export function currentTheme(): Theme {
    return resolveTheme(currentPreference())
}

// applyTheme 设置 html data-theme(驱动 CSS 变量)并持久化。
// 入参是"用户选择"(dark/light/system):system 会被解析成实际外观写入
// data-theme,但落盘仍是 system——这样系统偏好变化时才能继续跟随。
export function applyTheme(pref: ThemePreference) {
    document.documentElement.dataset.theme = resolveTheme(pref)
    try {
        localStorage.setItem(THEME_KEY, pref)
    } catch {
        // localStorage 不可用时静默降级
    }
    logger.info('theme', 'applied preference=%s -> %s', pref, document.documentElement.dataset.theme)
}

// notifyThemeChange 广播主题变化(xterm 终端组件订阅以动态更新配色)。
// 载荷是解析后的 Theme,订阅方无需再关心 system。
export function notifyThemeChange(theme: Theme) {
    window.dispatchEvent(new CustomEvent<Theme>(THEME_EVENT, { detail: theme }))
}

// onThemeChange 订阅主题变化,返回退订函数。
export function onThemeChange(cb: (theme: Theme) => void): () => void {
    const handler = (e: Event) => cb((e as CustomEvent<Theme>).detail)
    window.addEventListener(THEME_EVENT, handler)
    return () => window.removeEventListener(THEME_EVENT, handler)
}

// onSystemThemeChange 订阅系统配色偏好变化,返回退订函数。
// 只负责"通知",是否生效由调用方判断(用户选了亮/暗时不跟随系统)。
export function onSystemThemeChange(cb: (theme: Theme) => void): () => void {
    const mql = window.matchMedia(SYSTEM_DARK_QUERY)
    const handler = (e: MediaQueryListEvent) => cb(e.matches ? 'dark' : 'light')
    mql.addEventListener('change', handler)
    return () => mql.removeEventListener('change', handler)
}
