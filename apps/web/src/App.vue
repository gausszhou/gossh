<template>
  <div class="app">
    <!-- 访问令牌门禁:URL 无 ?token= 或令牌无效时弹出(Jupyter 式) -->
    <div v-if="tokenPromptOpen" class="token-gate">
      <div class="token-gate-card">
        <div class="token-gate-title">🔑 {{ t('token.title') }}</div>
        <div class="token-gate-hint">{{ t('token.hint') }}</div>
        <input
          v-model="tokenInput"
          class="token-gate-input"
          type="text"
          autocomplete="off"
          autocapitalize="off"
          spellcheck="false"
          :placeholder="t('token.placeholder')"
          @keyup.enter="submitToken"
        />
        <div v-if="tokenError" class="token-gate-error">{{ tokenError }}</div>
        <button class="token-gate-btn" @click="submitToken">{{ t('token.submit') }}</button>
      </div>
    </div>

    <!-- 左侧:主机列表栏(可折叠;展开/收起按钮在页签栏最左侧;右缘可拖拽调宽) -->
    <aside
      class="sidebar"
      :class="{ collapsed: sidebarCollapsed, resizing: resizingSidebar }"
      :style="sidebarStyle"
    >
      <div class="sidebar-inner" :style="sidebarInnerStyle">
        <HostList
          :hosts="hosts"
          @connect="connectHost"
          @forwards="openHostForwards"
          @edit="editHost"
          @delete="refreshHosts"
          @new-host="openHostForm(null)"
        />
      </div>
      <span
        v-if="!sidebarCollapsed"
        class="sidebar-resizer"
        :title="t('host.resize')"
        @pointerdown="startSidebarResize"
        @dblclick="resetSidebarWidth"
      ></span>
    </aside>

    <!-- 右侧:页签区 + 工具体栏 -->
    <div class="main">
      <TabBar
        :tabs="tabs"
        :active-id="activeTabId"
        :collapsed="sidebarCollapsed"
        @open="(id) => (activeTabId = id)"
        @close="closeTab"
        @reorder="onReorder"
        @settings="settingsOpen = true"
        @toggle-sidebar="sidebarCollapsed = !sidebarCollapsed"
      />

      <div class="content">
        <template v-for="tab in tabs" :key="tab.id">
          <SshView
            v-if="tab.kind === 'ssh'"
            v-show="tab.id === activeTabId"
            :ref="(el) => setViewRef(tab.id, el)"
            :session-id="tab.sessionId!"
            :host-id="tab.hostId!"
            :host-label="tab.hostLabel || tab.title"
            :active="tab.id === activeTabId"
            :latency="tab.latency ?? null"
            @close="closeTab(tab)"
            @latency="onLatency(tab, $event)"
            @conn="onConn(tab, $event)"
            @tab-title="(ti) => onTabTitle(tab, ti)"
            @credential-required="(msg) => onPaneCredentialRequired(tab, msg)"
          />
        </template>

        <!-- 空态 -->
        <div v-if="!tabs.length" class="content-empty">
          <div v-if="bootError" class="empty-error">{{ bootError }}</div>
          <div v-else-if="booting" class="empty-loading">
            <span class="spinner" aria-hidden="true"></span>
            <span class="empty-loading-text">{{ t('empty.loading') }}</span>
          </div>
          <div v-else class="empty-card">
            <Terminal :size="42" class="empty-card-icon" />
            <span class="empty-card-title">{{ t('empty.title') }}</span>
            <!-- 提示里内联一个"迷你按钮":图标用与行内按钮同源的 lucide 组件
                 (字体字形 ▶ 跨平台形态/基线不一致),文字用按钮名,便于对照识别 -->
            <span class="empty-card-hint">
              {{ t('empty.hintPre') }}<span class="empty-hint-btn"><Play :size="12" aria-hidden="true" />{{ t('host.act.connect') }}</span>{{ t('empty.hintPost') }}
            </span>
          </div>
        </div>
      </div>
    </div>

    <!-- 轻提示 -->
    <Transition name="toast">
      <div v-if="toast" class="toast">{{ toast }}</div>
    </Transition>

    <!-- 弹窗 -->
    <HostFormModal
      :open="hostFormOpen"
      :host="hostFormHost"
      :hosts="hosts"
      @close="hostFormOpen = false"
      @saved="onHostSaved"
    />
    <CredentialsModal
      :open="credOpen"
      :message="credMessage"
      :busy="credBusy"
      :error="credError"
      @submit="onCredSubmit"
      @close="credOpen = false"
    />
    <SettingsModal
      :open="settingsOpen"
      :theme="themePref"
      :resolved-theme="theme"
      @close="settingsOpen = false"
      @theme="onThemeSelect"
      @title-saved="onPageTitleSaved"
    />

    <!-- 主机级端口转发管理(持久定义,连上即生效) -->
    <HostForwardsModal
      v-if="hostForwardsHost"
      :host="hostForwardsHost"
      @close="hostForwardsHost = null"
      @saved="onHostForwardsSaved"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref, watch } from 'vue'
import { Play, Terminal } from 'lucide-vue-next'
import TabBar from './components/TabBar.vue'
import HostList from './components/HostList.vue'
import HostFormModal from './components/HostFormModal.vue'
import CredentialsModal from './components/CredentialsModal.vue'
import HostForwardsModal from './components/HostForwardsModal.vue'
import SettingsModal from './components/SettingsModal.vue'
import SshView from './components/SshView.vue'
import {
    listHosts, createSession, checkSessions, destroySession, updateSessionTitle,
    isCredentialError, getPageTitle, getToken, APIError,
} from './utils/api'
import {
    applyTheme, currentPreference, onSystemThemeChange, systemDarkQueryMatches,
    notifyThemeChange, type Theme, type ThemePreference,
} from './utils/theme'
import { t } from './utils/i18n'
import {
    loadManifest, upsertManifest, removeFromManifest, generateSessionID,
} from './utils/manifest'
import { logger } from './utils/logger'
import { loadTabOrder, saveTabOrder } from './utils/tabOrder'
import type { AppTab, CredentialPayload, Host, StateDescription } from './utils/types'

// ── 主题 / 设置 ──
// themePref 是用户选择(可能为 system);systemDark 是系统配色偏好的实时快照;
// theme 是解析后实际生效的亮/暗外观,由前两者派生——这样系统偏好变化时,
// 设置弹窗里的"当前生效"提示也会跟着变。
const themePref = ref<ThemePreference>(currentPreference())
const systemDark = ref(systemDarkQueryMatches())
const theme = computed<Theme>(() =>
    themePref.value === 'system' ? (systemDark.value ? 'dark' : 'light') : themePref.value,
)
const settingsOpen = ref(false)
// 系统配色偏好监听(挂载时建立,卸载时拆除)
let unsubscribeSystemTheme: (() => void) | null = null

// 主题变化统一出口:写 data-theme + 持久化偏好 + 广播给 xterm。
// 同时观察偏好与解析后的主题:偏好变了但解析结果没变(如 system→dark,
// 系统本来就是暗色)也必须落盘,否则偏好会在下次启动时丢失。
watch([themePref, theme], ([pref, resolved], [prevPref, prevResolved]) => {
    if (pref === prevPref && resolved === prevResolved) return
    applyTheme(pref)
    if (resolved !== prevResolved) notifyThemeChange(resolved)
    logger.info('app', 'theme -> %s (preference=%s)', resolved, pref)
})

// 手动选择偏好:watch 立即把新主题应用出去(含 system 的即时解析)。
function onThemeSelect(next: ThemePreference) {
    themePref.value = next
}

function onPageTitleSaved(title: string) {
    document.title = title || 'GoSSH'
}

// ── 主机清单 ──
const hosts = ref<Host[]>([])
const sideCollapsed = ref(false)
const sidebarCollapsed = computed({
    get: () => sideCollapsed.value,
    set: (v: boolean) => (sideCollapsed.value = v),
})

// ── 侧栏宽度(可拖拽调整,localStorage 持久化) ──
const SIDEBAR_KEY = 'gossh.sidebarWidth'
const SIDEBAR_DEFAULT = 264
const SIDEBAR_MIN = 200
const SIDEBAR_MAX = 560
// 注意:200 是最小宽度而非上限;vmin 让窄屏(如侧栏占满半屏)也能用
const sidebarMax = () => Math.min(SIDEBAR_MAX, Math.max(SIDEBAR_MIN, Math.round(window.innerWidth * 0.6)))

function loadSidebarWidth(): number {
    try {
        const v = Number(localStorage.getItem(SIDEBAR_KEY))
        if (Number.isFinite(v) && v >= SIDEBAR_MIN && v <= SIDEBAR_MAX) return Math.round(v)
    } catch {
        // localStorage 不可用时用默认值
    }
    return SIDEBAR_DEFAULT
}

const sidebarWidth = ref(loadSidebarWidth())
const resizingSidebar = ref(false)
const sidebarStyle = computed(() =>
    sidebarCollapsed.value ? undefined : { width: `${sidebarWidth.value}px` },
)
// 内层固定同宽,折叠动画时内容不被压缩换行
const sidebarInnerStyle = { width: '100%' }

function saveSidebarWidth() {
    try {
        localStorage.setItem(SIDEBAR_KEY, String(sidebarWidth.value))
    } catch {
        // 忽略持久化失败
    }
}

// 拖拽调宽:pointermove 里按指针 X 与侧栏左边界求差,并夹在 [MIN, sidebarMax] 内
function startSidebarResize(e: PointerEvent) {
    if (sidebarCollapsed.value) return
    const aside = (e.currentTarget as HTMLElement).parentElement
    if (!aside) return
    const left = aside.getBoundingClientRect().left
    resizingSidebar.value = true
    const target = e.currentTarget as HTMLElement
    target.setPointerCapture?.(e.pointerId)

    const onMove = (ev: PointerEvent) => {
        const next = Math.round(ev.clientX - left)
        sidebarWidth.value = Math.min(sidebarMax(), Math.max(SIDEBAR_MIN, next))
    }
    const onUp = () => {
        resizingSidebar.value = false
        window.removeEventListener('pointermove', onMove)
        window.removeEventListener('pointerup', onUp)
        saveSidebarWidth()
        logger.info('app', 'sidebar width set to %d', sidebarWidth.value)
    }
    window.addEventListener('pointermove', onMove)
    window.addEventListener('pointerup', onUp)
    e.preventDefault()
}

// 双击分隔条:恢复默认宽度
function resetSidebarWidth() {
    sidebarWidth.value = SIDEBAR_DEFAULT
    saveSidebarWidth()
}

function hostLabel(h: Host): string {
    const port = h.port && h.port !== 22 ? `:${h.port}` : ''
    return `${h.user}@${h.address}${port}`
}

// hostName 渲染主机显示名:内置本地服务器的名字由 i18n 决定(服务端只给
// 语言无关的 fallback,见 internal/host.Local),用户主机用记录里的名字。
function hostName(h: Host): string {
    return h.builtin ? t('host.local') : h.name
}

async function refreshHosts() {
    try {
        hosts.value = await listHosts()
    } catch (err) {
        logger.warn('app', 'failed to list hosts: %s', err)
        showToast(err instanceof Error ? err.message : String(err))
    }
}

// ── 页签 ──
const tabs = ref<AppTab[]>([])
const activeTabId = ref('')
// 常驻视图实例(按页签 id;SSH 页签暴露 reattach 供凭据重建后重连)
const viewRefs = ref<Record<string, unknown>>({})
let tabSeq = 0

function setViewRef(id: string, el: unknown) {
    if (el) viewRefs.value[id] = el
    else delete viewRefs.value[id]
}

function pushTab(tab: AppTab, activate = true) {
    tabs.value.push(tab)
    applyTabOrder()
    if (activate) activeTabId.value = tab.id
}

// ── 页签顺序(拖拽排序,localStorage 持久化) ──
// gotty 同款语义:已知顺序的页签按 gossh.tabOrder 排列,
// 未记录顺序的新页签按创建序追加在末尾。
const tabOrder = ref<string[]>(loadTabOrder())

// pushTab / 外部恢复页签后按持久化顺序重排;unknown 保持相对创建序
function applyTabOrder() {
    if (!tabOrder.value.length) return
    const known = tabs.value
        .filter((t) => tabOrder.value.includes(t.id))
        .sort((a, b) => tabOrder.value.indexOf(a.id) - tabOrder.value.indexOf(b.id))
    const unknown = tabs.value.filter((t) => !tabOrder.value.includes(t.id))
    tabs.value = [...known, ...unknown]
}

// TabBar emit('reorder', from, to):与 gotty onDrop 相同的移动公式
function onReorder(from: number, to: number) {
    if (from === -1 || from === to || from >= tabs.value.length) return
    const next = [...tabs.value]
    const [moved] = next.splice(from, 1)
    next.splice(from < to ? to - 1 : to, 0, moved)
    tabs.value = next
    tabOrder.value = next.map((t) => t.id)
    saveTabOrder(tabOrder.value)
    logger.info('app', 'tabs reordered, saved %d entries', tabOrder.value.length)
}

// ── SSH 页签 ──
function addSshTab(s: StateDescription, host: Host, label: string, activate = true, title?: string) {
    const tab: AppTab = {
        id: s.id,
        kind: 'ssh',
        title: title || hostName(host),
        sessionId: s.id,
        hostId: host.id,
        hostName: hostName(host),
        hostLabel: label,
        alive: true,
        connected: false,
        createdAt: Date.now(),
    }
    upsertManifest({
        id: s.id,
        hostId: host.id,
        createdAt: tab.createdAt,
        lastSeen: Date.now(),
        ...(tab.title ? { title: tab.title } : {}),
    })
    pushTab(tab, activate)
}

// ── 连接流程:生成 16 位 base36 id → 创建会话 → 开页签 ──
async function connectHost(host: Host) {
    logger.info('app', 'connect host=%s (%s)', host.id, host.name)
    const id = generateSessionID()
    const label = hostLabel(host)
    try {
        const s = await createSession({ host_id: host.id, id })
        addSshTab(s, host, label)
    } catch (err) {
        if (isCredentialError(err)) {
            openCredPrompt('connect', host.id, id, host.name, (s) => addSshTab(s, host, label))
            return
        }
        showToast(err instanceof Error ? err.message : String(err))
    }
}

// ── 凭据弹窗 ──
interface PendingConnectCred {
    mode: 'connect'
    hostId: string
    sessionId: string
    message: string
    then: (s: StateDescription) => void
}

interface PendingRebuildCred {
    mode: 'rebuild'
    hostId: string
    sessionId: string
    message: string
}

type PendingCred = PendingConnectCred | PendingRebuildCred

const credOpen = ref(false)
const credBusy = ref(false)
const credError = ref('')
const pendingCred = ref<PendingCred | null>(null)
const credMessage = computed(() => pendingCred.value?.message || '')

function openCredPrompt(
    mode: 'connect',
    hostId: string,
    sessionId: string,
    hostName: string,
    then: (s: StateDescription) => void,
) {
    pendingCred.value = {
        mode,
        hostId,
        sessionId,
        message: t('cred.message').replace('%s', hostName),
        then,
    }
    credError.value = ''
    credBusy.value = false
    credOpen.value = true
}

// TerminalPane 重建会话缺凭据(会话消失后重连)
function onPaneCredentialRequired(tab: AppTab, message: string) {
    if (!tab.sessionId || !tab.hostId) return
    pendingCred.value = {
        mode: 'rebuild',
        hostId: tab.hostId,
        sessionId: tab.sessionId,
        message: t('cred.retryMessage'),
    }
    credError.value = ''
    credBusy.value = false
    credOpen.value = true
}

// 凭据提交:用 {password, passphrase, save_*} 重试创建会话
function onCredSubmit(payload: CredentialPayload) {
    const pending = pendingCred.value
    if (!pending) return
    credBusy.value = true
    credError.value = ''
    void (async () => {
        try {
            const s = await createSession({
                host_id: pending.hostId,
                id: pending.sessionId,
                password: payload.password,
                passphrase: payload.passphrase,
                save_password: payload.savePassword,
                save_passphrase: payload.savePassphrase,
            })
            if (pending.mode === 'connect') {
                pending.then(s)
            } else {
                // 重建成功:重新附着对应 SSH 视图
                const v = viewRefs.value[pending.sessionId] as { reattach?: () => void } | undefined
                v?.reattach?.()
            }
            credOpen.value = false
            pendingCred.value = null
        } catch (err) {
            logger.warn('app', 'credential retry failed: %s', err)
            if (isCredentialError(err)) {
                credError.value = err instanceof Error ? err.message : String(err) || t('cred.failed')
                // 保持弹窗打开,允许重试
            } else {
                credOpen.value = false
                pendingCred.value = null
                showToast(err instanceof Error ? err.message : String(err))
            }
        } finally {
            credBusy.value = false
        }
    })()
}

// ── 页签关闭 / 销毁 ──
function closeTab(tab: AppTab) {
    if (tab.kind === 'ssh' && tab.sessionId) {
        // 页签关闭 = 销毁会话(后端语义)
        logger.info('app', 'close ssh tab -> destroy session=%s', tab.sessionId)
        void destroySession(tab.sessionId)
        removeFromManifest(tab.sessionId)
        dropTabsBySession(tab.sessionId)
    } else {
        dropTab(tab.id)
    }
}

function dropTabsBySession(sessionId: string) {
    const doomed = tabs.value.filter((tb) => tb.sessionId === sessionId).map((tb) => tb.id)
    for (const id of doomed) dropTab(id)
}

function dropTab(id: string) {
    tabs.value = tabs.value.filter((tb) => tb.id !== id)
    delete viewRefs.value[id]
    if (activeTabId.value === id) {
        activeTabId.value = tabs.value.length ? tabs.value[tabs.value.length - 1].id : ''
    }
}

// ── 会话状态轮询(manifest 清单 → status 批量查) ──
const POLL_PERIOD_MS = 2000
let pollTimer: ReturnType<typeof setInterval> | null = null

async function refreshStatus() {
    const entries = loadManifest()
    if (entries.length === 0) return
    const ids = entries.map((e) => e.id)
    try {
        const alive = await checkSessions(ids)
        const aliveMap = new Map(alive.map((s) => [s.id, s]))

        // 更新页签存活标记 + 清理服务端已死的会话页签
        const deadIds = new Set<string>()
        for (const tab of tabs.value) {
            if (tab.kind === 'ssh' && tab.sessionId) {
                tab.alive = aliveMap.has(tab.sessionId)
                if (!tab.alive) deadIds.add(tab.sessionId)
            }
        }
        for (const dead of deadIds) {
            logger.info('app', 'session died server-side, closing tabs bound to %s', dead)
            removeFromManifest(dead)
            dropTabsBySession(dead)
        }

        // 清单同步(仅剩存活条目)
        const stale = entries.filter((e) => !aliveMap.has(e.id))
        for (const e of stale) removeFromManifest(e.id)
    } catch {
        // 服务端不可用:保留现状
    }
}

function startPolling() {
    if (pollTimer) return
    pollTimer = setInterval(() => void refreshStatus(), POLL_PERIOD_MS)
}

// ── 页签事件(SSH 视图上报) ──
function onLatency(tab: AppTab, ms: number | null) {
    tab.latency = ms ?? undefined
}

function onConn(tab: AppTab, connected: boolean) {
    tab.connected = connected
}

// 程序标题(OSC 0/2):更新页签标题 + 清单 + 服务端持久化
function onTabTitle(tab: AppTab, title: string) {
    if (!title || !tab.sessionId) return
    tab.title = title
    const entry = loadManifest().find((e) => e.id === tab.sessionId)
    if (entry) {
        upsertManifest({ ...entry, title })
    }
    void updateSessionTitle(tab.sessionId, title)
}

// ── 主机表单 ──
const hostFormOpen = ref(false)
const hostFormHost = ref<Host | null>(null)

function openHostForm(host: Host | null) {
    hostFormHost.value = host
    hostFormOpen.value = true
}

function editHost(host: Host) {
    openHostForm(host)
}

async function onHostSaved() {
    hostFormOpen.value = false
    await refreshHosts()
}

// ── 轻提示 ──
const toast = ref('')
let toastTimer: ReturnType<typeof setTimeout> | null = null

function showToast(message: string) {
    toast.value = message
    if (toastTimer) clearTimeout(toastTimer)
    toastTimer = setTimeout(() => {
        toast.value = ''
    }, 4000)
}

// ── 主机级端口转发(持久定义,会话连接时自动应用) ──
const hostForwardsHost = ref<Host | null>(null)

function openHostForwards(host: Host) {
    hostForwardsHost.value = host
}

async function onHostForwardsSaved() {
    hostForwardsHost.value = null
    await refreshHosts()
}

// ── 访问令牌门禁 ──
// 无 ?token=(或令牌失效 401)时弹出输入框;保存后重载页面。
const tokenPromptOpen = ref(false)
const tokenInput = ref('')
const tokenError = ref('')

function openTokenPrompt(message = '') {
    tokenError.value = message
    tokenPromptOpen.value = true
}

function submitToken() {
    const tk = tokenInput.value.trim()
    if (!tk) {
        tokenError.value = t('token.invalid')
        return
    }
    try {
        sessionStorage.setItem('gossh.token', tk)
    } catch {
        // 忽略持久化失败
    }
    location.reload()
}

// ── 启动 ──
const booting = ref(true)
const bootError = ref('')

onMounted(async () => {
    document.title = 'GoSSH'

    // 跟随系统:仅当用户选择为 system 时,系统配色变化才实时生效
    // (选了亮/暗即视为固定,用户的手动选择优先于系统)。这里只更新
    // 系统快照,应用/广播统一由 theme 的 watch 完成。
    unsubscribeSystemTheme = onSystemThemeChange((resolved) => {
        systemDark.value = resolved === 'dark'
    })

    if (!getToken()) {
        // URL 没有令牌:直接引导输入,跳过会 401 的刷新
        window.setTimeout(() => openTokenPrompt(), 100)
        return
    }

    await refreshHosts()

    // 部署级页面标题(浏览器标签页)
    try {
        const title = await getPageTitle()
        if (title) document.title = title
    } catch {
        // 忽略
    }

    try {
        const entries = loadManifest()
        if (entries.length > 0) {
            logger.info('app', 'boot: manifest entries=%d', entries.length)
            // 先轮询一次:清单清掉服务端已死的条目,再据存活条目重建页签
            await refreshStatus()
            const cleaned = loadManifest()
            for (const e of cleaned) {
                const host = hosts.value.find((h) => h.id === e.hostId)
                pushTab(
                    {
                        id: e.id,
                        kind: 'ssh',
                        title: e.title || (host ? hostName(host) : e.hostId),
                        sessionId: e.id,
                        hostId: e.hostId,
                        hostName: host ? hostName(host) : e.hostId,
                        hostLabel: host ? hostLabel(host) : e.hostId,
                        alive: true,
                        connected: false,
                        createdAt: e.createdAt,
                    },
                    false,
                )
            }
            // 打开最近存活会话(视图按需挂载)
            const recent = [...cleaned].sort((a, b) => b.lastSeen - a.lastSeen)[0]
            if (recent) activeTabId.value = recent.id
        }
    } catch (err) {
        logger.warn('app', 'boot error: %s', err)
        bootError.value = err instanceof Error ? err.message : String(err)
    } finally {
        booting.value = false
        startPolling()
    }
})

onBeforeUnmount(() => {
    if (pollTimer) clearInterval(pollTimer)
    unsubscribeSystemTheme?.()
})
</script>

<style>
html, body, #app {
    margin: 0;
    padding: 0;
    height: 100%;
    width: 100%;
    background: var(--bg-app);
}

body {
    /* VSCode 默认 UI 字体栈(Windows: Segoe UI, macOS: system-ui, Linux: Ubuntu/Sans) */
    font-family: 'Segoe UI', system-ui, -apple-system, BlinkMacSystemFont,
        'Ubuntu', 'Droid Sans', sans-serif;
    font-size: 13px;
    color: var(--fg);
    -webkit-font-smoothing: antialiased;
    -moz-osx-font-smoothing: grayscale;
}

* {
    box-sizing: border-box;
}

/* 轻提示过渡 */
.toast-enter-active,
.toast-leave-active {
    transition: opacity 0.2s, transform 0.2s;
}

.toast-enter-from,
.toast-leave-to {
    opacity: 0;
    transform: translateY(6px);
}
</style>

<style scoped>
.app {
    display: flex;
    height: 100vh;
    width: 100vw;
    background: var(--bg-app);
    overflow: hidden;
}

.sidebar {
    position: relative;
    flex: 0 0 auto;
    width: 264px; /* 默认宽度;实际宽度由内联 style 覆盖(可拖拽调整) */
    height: 100%;
    overflow: hidden;
    background: var(--bg-bar);
    border-right: 1px solid var(--bg-bar-border);
    transition: width 0.13s ease-out;
}

/* 拖拽调宽期间:关掉过渡,指针跟随才不粘手 */
.sidebar.resizing {
    transition: none;
    user-select: none;
}

.sidebar.collapsed {
    width: 0;
    border-right: none;
}

.sidebar-inner {
    width: 100%;
    height: 100%;
    display: flex;
    flex-direction: column;
}

/* ── 宽度拖拽分隔条(悬停高亮,双击复位默认宽度) ── */
.sidebar-resizer {
    position: absolute;
    top: 0;
    right: 0;
    width: 5px;
    height: 100%;
    z-index: 5;
    cursor: col-resize;
    background: transparent;
    touch-action: none;
    transition: background 0.12s ease-out;
}

.sidebar-resizer:hover,
.sidebar-resizer:active {
    background: var(--accent);
}

.main {
    flex: 1 1 auto;
    min-width: 0;
    display: flex;
    flex-direction: column;
    height: 100%;
}

.content {
    flex: 1 1 auto;
    min-height: 0;
    display: flex;
    background: var(--bg-app);
}

.content > * {
    min-width: 0;
    min-height: 0;
}

/* ── 空态(VSCode 欢迎视图:无边框,居中图标 + 主副文案) ── */
.content-empty {
    flex: 1;
    display: flex;
    align-items: center;
    justify-content: center;
    color: var(--fg-muted);
}

.empty-loading {
    display: flex;
    align-items: center;
    gap: 10px;
    font-size: 13px;
}

.empty-loading-text {
    color: var(--fg-hint);
}

.spinner {
    width: 14px;
    height: 14px;
    border: 1.5px solid var(--border-input);
    border-top-color: var(--accent);
    border-radius: 50%;
    animation: spin 0.8s linear infinite;
    flex: 0 0 auto;
}

@keyframes spin {
    to {
        transform: rotate(360deg);
    }
}

.empty-card {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 10px;
    padding: 24px 32px;
    max-width: 380px;
    color: var(--fg);
    font-family: inherit;
    text-align: center;
}

.empty-card-icon {
    color: var(--fg-hint);
    opacity: 0.55;
}

.empty-card-title {
    font-size: 16px;
    font-weight: 300;
    line-height: 1.4;
    color: var(--fg-bright);
}

.empty-card-hint {
    display: inline-flex;
    align-items: center;
    gap: 3px;
    font-size: 13px;
    line-height: 1.5;
    color: var(--fg-hint);
}

/* 行内"迷你按钮":与主机行内的连接按钮同款(绿色图标 + 名称) */
.empty-hint-btn {
    display: inline-flex;
    align-items: center;
    gap: 3px;
    padding: 1px 6px;
    border: 1px solid color-mix(in srgb, var(--net-good) 45%, transparent);
    border-radius: var(--radius-md);
    color: var(--net-good);
    font-size: 12px;
    line-height: 1.5;
    white-space: nowrap;
}

.empty-error {
    max-width: 420px;
    padding: 10px 16px;
    color: var(--net-bad);
    font-size: 13px;
    text-align: center;
    line-height: 1.6;
}

/* ── 轻提示(VSCode notification toaster 观感) ── */
.toast {
    position: fixed;
    right: 16px;
    bottom: 16px;
    z-index: 2000;
    max-width: 420px;
    padding: 10px 14px;
    background: var(--bg-dialog);
    border: 1px solid var(--border-dialog);
    border-radius: var(--radius-md);
    box-shadow: var(--shadow-widget);
    color: var(--fg);
    font-size: 13px;
    line-height: 1.5;
    word-break: break-word;
}

/* ── 访问令牌门禁 ── */
.token-gate {
    position: fixed;
    inset: 0;
    z-index: 1000;
    display: flex;
    align-items: center;
    justify-content: center;
    background: var(--overlay);
}

.token-gate-card {
    width: 440px;
    max-width: calc(100vw - 40px);
    background: var(--bg-dialog);
    border: 1px solid var(--border-dialog);
    border-radius: var(--radius-lg);
    padding: 22px;
    display: flex;
    flex-direction: column;
    gap: 12px;
    box-shadow: var(--shadow-dialog);
}

.token-gate-title {
    font-size: 15px;
    font-weight: 500;
    color: var(--fg-bright);
}

.token-gate-hint {
    font-size: 12px;
    line-height: 1.6;
    color: var(--fg-muted);
}

.token-gate-input {
    width: 100%;
    box-sizing: border-box;
    height: 26px;
    padding: 0 8px;
    border-radius: var(--radius-sm);
    border: 1px solid var(--border-input);
    background: var(--bg-input);
    color: var(--fg);
    font-family: 'SF Mono', Consolas, 'DejaVu Sans Mono', monospace;
    font-size: 13px;
    outline: none;
}

.token-gate-input:focus {
    border-color: var(--focus-border);
}

.token-gate-error {
    font-size: 12px;
    color: var(--net-bad);
}

.token-gate-btn {
    align-self: flex-end;
    height: 26px;
    padding: 0 16px;
    border-radius: var(--radius-sm);
    border: none;
    background: var(--accent);
    color: #ffffff;
    font-size: 13px;
    font-family: inherit;
    cursor: pointer;
}

.token-gate-btn:hover {
    background: var(--accent-hover);
}
</style>