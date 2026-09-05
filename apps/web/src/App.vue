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

    <!-- 左侧:主机列表栏(可折叠) -->
    <aside class="sidebar" :class="{ collapsed: sidebarCollapsed }">
      <div class="sidebar-inner">
        <HostList
          :hosts="hosts"
          @connect="connectHost"
          @forwards="openHostForwards"
          @edit="editHost"
          @delete="refreshHosts"
        />
      </div>
    </aside>

    <!-- 中间:SFTP 文件列表(绑定当前活动 SSH 会话,自动开启;SFTP_ENABLED 编译期门控) -->
    <aside v-if="SFTP_ENABLED && activeSshTab" class="sftp-panel" :class="{ collapsed: sftpPanelCollapsed }">
      <div class="sftp-panel-header">
        <span class="sftp-panel-title">📁 {{ activeSshTab.hostLabel || activeSshTab.title }}</span>
        <span class="sftp-panel-actions">
          <button class="sftp-panel-run" :title="t('host.act.run')" @click="runHostOfActiveWorkbench">▶</button>
          <button
          class="sftp-panel-toggle"
          :title="sftpPanelCollapsed ? t('sftp.expand') : t('sftp.collapse')"
          @click="sftpPanelCollapsed = !sftpPanelCollapsed"
        >{{ sftpPanelCollapsed ? '◂' : '▸' }}</button>
        </span>
      </div>
      <div v-if="!sftpPanelCollapsed" class="sftp-panel-body">
        <SFTPView :session-id="activeSshTab.sessionId!" :active="true" :key="activeSshTab.sessionId" />
      </div>
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
        @new-host="openHostForm(null)"
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
            @forwards="openForwardModal(tab)"
          />
          <RunView
            v-else-if="tab.kind === 'run'"
            v-show="tab.id === activeTabId"
            :ref="(el) => setViewRef(tab.id, el)"
            :run="tab.run!"
            :active="tab.id === activeTabId"
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
            <span class="empty-card-icon">⌨</span>
            <span class="empty-card-title">{{ t('empty.title') }}</span>
            <span class="empty-card-hint">{{ t('empty.hint') }}</span>
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
    <RunModal
      :open="runModalOpen"
      :host="runModalHost"
      @close="runModalOpen = false"
      @run="onRunDone"
    />
    <CredentialsModal
      :open="credOpen"
      :message="credMessage"
      :busy="credBusy"
      :error="credError"
      @submit="onCredSubmit"
      @close="credOpen = false"
    />
    <ForwardModal
      :open="forwardOpen"
      :session-id="forwardSessionId"
      @close="forwardOpen = false"
    />
    <SettingsModal
      :open="settingsOpen"
      :theme="theme"
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
import { computed, defineAsyncComponent, onMounted, onBeforeUnmount, ref } from 'vue'
import TabBar from './components/TabBar.vue'
import HostList from './components/HostList.vue'
import HostFormModal from './components/HostFormModal.vue'
import RunModal from './components/RunModal.vue'
import CredentialsModal from './components/CredentialsModal.vue'
import ForwardModal from './components/ForwardModal.vue'
import HostForwardsModal from './components/HostForwardsModal.vue'
import SettingsModal from './components/SettingsModal.vue'
import SshView from './components/SshView.vue'
// SFTP 面板按编译期开关加载:VITE_SFTP=1 时懒加载组件,否则不参与渲染
// (与后端 -tags sftp 同源,见 utils/features.ts 与 Makefile SFTP)
import { SFTP_ENABLED } from './utils/features'
const SFTPView = SFTP_ENABLED
    ? defineAsyncComponent(() => import('./components/SFTPView.vue'))
    : null
import RunView from './components/RunView.vue'
import {
    listHosts, createSession, checkSessions, destroySession, updateSessionTitle,
    isCredentialError, getPageTitle, getToken, APIError,
} from './utils/api'
import { applyTheme, currentTheme, notifyThemeChange, type Theme } from './utils/theme'
import { t } from './utils/i18n'
import {
    loadManifest, upsertManifest, removeFromManifest, generateSessionID, type ManifestEntry,
} from './utils/manifest'
import { logger } from './utils/logger'
import { loadTabOrder, saveTabOrder } from './utils/tabOrder'
import type { AppTab, CredentialPayload, Host, StateDescription } from './utils/types'

// ── 主题 / 设置 ──
const theme = ref<Theme>(currentTheme())
const settingsOpen = ref(false)

function onThemeSelect(next: Theme) {
    applyTheme(next)
    notifyThemeChange(next)
    theme.value = next
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

function hostLabel(h: Host): string {
    const port = h.port && h.port !== 22 ? `:${h.port}` : ''
    return `${h.user}@${h.address}${port}`
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
        title: title || host.name,
        sessionId: s.id,
        hostId: host.id,
        hostName: host.name,
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

// ── SFTP 流程:绑定存活会话(无则静默创建) ──
async function findAliveSessionForHost(hostId: string): Promise<string | null> {
    // 已打开的 SSH 页签(存活)
    for (const tab of tabs.value) {
        if (tab.kind === 'ssh' && tab.hostId === hostId && tab.alive !== false && tab.sessionId) {
            return tab.sessionId
        }
    }
    // 本机清单 + status 轮询
    const mine = loadManifest()
        .filter((e) => e.hostId === hostId)
        .map((e) => e.id)
    if (mine.length > 0) {
        try {
            const alive = await checkSessions(mine)
            if (alive.length > 0) return alive[0].id
        } catch {
            // 服务端不可用,按无存活处理
        }
    }
    return null
}


// ── 运行命令流程 ──
const runModalOpen = ref(false)
const runModalHost = ref<Host | null>(null)

function openRunModal(host: Host) {
    runModalHost.value = host
    runModalOpen.value = true
}

function onRunDone(result: import('./utils/types').RunResult) {
    runModalOpen.value = false
    const host = runModalHost.value
    const title = host ? `${host.name} · ${t('run.tabSuffix')}` : `${t('run.tabSuffix')} ${result.host_id}`
    pushTab({
        id: `run-${++tabSeq}`,
        kind: 'run',
        title,
        hostId: result.host_id,
        hostName: result.name,
        run: result,
        command: result.command,
        createdAt: Date.now(),
    })
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
        // 页签关闭 = 销毁会话(后端语义);SFTP 页签绑定同一会话,一并关闭
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

// ── 端口转发 ──
const forwardOpen = ref(false)
const forwardSessionId = ref('')

function openForwardModal(tab: AppTab) {
    if (!tab.sessionId) return
    forwardSessionId.value = tab.sessionId
    forwardOpen.value = true
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

// 中栏 ▶ 运行命令:针对当前工作区主机
function runHostOfActiveWorkbench() {
    const tab = activeSshTab.value
    if (!tab?.hostId) return
    const host = hosts.value.find((h) => h.id === tab.hostId)
    if (host) openRunModal(host)
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

// ── SFTP 中栏(绑定活动 SSH 会话,三栏布局) ──
const sftpPanelCollapsed = ref(false)

// 活动 SSH 页签:中栏 SFTP 的数据源(host 工作区)
const activeSshTab = computed(() => {
    const tab = tabs.value.find((t) => t.id === activeTabId.value)
    return tab && tab.kind === 'ssh' ? tab : undefined
})

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
                        title: e.title || host?.name || e.hostId,
                        sessionId: e.id,
                        hostId: e.hostId,
                        hostName: host?.name || e.hostId,
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
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', 'Helvetica Neue',
        sans-serif;
    color: var(--fg);
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
    flex: 0 0 auto;
    width: 264px;
    height: 100%;
    overflow: hidden;
    background: var(--bg-bar);
    border-right: 1px solid var(--bg-bar-border);
    transition: width 0.18s ease;
}

.sidebar.collapsed {
    width: 0;
    border-right: none;
}

.sidebar-inner {
    width: 264px;
    height: 100%;
    display: flex;
    flex-direction: column;
}

/* ── 中间 SFTP 栏(与侧栏同风格,固定宽度,内部滚动) ── */
.sftp-panel {
    flex: 0 0 auto;
    width: 340px;
    height: 100%;
    display: flex;
    flex-direction: column;
    background: var(--bg-bar);
    border-right: 1px solid var(--bg-bar-border);
    min-width: 0;
}

.sftp-panel.collapsed {
    width: 40px;
}

.sftp-panel-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    height: 32px;
    padding: 0 8px;
    border-bottom: 1px solid var(--bg-bar-border);
    font-size: 12px;
    color: var(--fg-muted);
    flex: 0 0 auto;
}

.sftp-panel-title {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.sftp-panel-toggle {
    background: none;
    border: none;
    color: var(--fg-dim);
    cursor: pointer;
    font-size: 12px;
    padding: 2px 6px;
    border-radius: 3px;
}

.sftp-panel-toggle:hover {
    background: var(--bg-tab-hover);
    color: var(--fg-bright);
}

.sftp-panel-body {
    flex: 1 1 auto;
    min-height: 0;
    display: flex;
}

.sftp-panel-body > * {
    flex: 1 1 auto;
    min-width: 0;
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

/* ── 空态 ── */
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
    border: 2px solid var(--border-tab);
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
    gap: 6px;
    padding: 28px 44px;
    min-width: 280px;
    background: var(--bg-dialog);
    border: 1px dashed var(--border-tab);
    border-radius: 8px;
    color: var(--fg);
    font-family: inherit;
}

.empty-card-icon {
    font-size: 26px;
    line-height: 1;
    color: var(--fg-hint);
}

.empty-card-title {
    font-size: 15px;
    line-height: 1.4;
    color: var(--fg-bright);
}

.empty-card-hint {
    font-size: 12px;
    line-height: 1.4;
    color: var(--fg-muted);
}

.empty-error {
    max-width: 320px;
    padding: 10px 16px;
    color: #f48771;
    font-size: 13px;
    text-align: center;
    line-height: 1.6;
}

/* ── 轻提示 ── */
.toast {
    position: fixed;
    right: 16px;
    bottom: 16px;
    z-index: 2000;
    max-width: 420px;
    padding: 10px 14px;
    background: var(--bg-dialog);
    border: 1px solid var(--border-dialog);
    border-radius: 6px;
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.5);
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
    background: rgba(0, 0, 0, 0.55);
}

.token-gate-card {
    width: 420px;
    max-width: calc(100vw - 40px);
    background: var(--bg-panel, #161b22);
    border: 1px solid var(--border-tab, #30363d);
    border-radius: 8px;
    padding: 24px;
    display: flex;
    flex-direction: column;
    gap: 12px;
    box-shadow: 0 12px 40px rgba(0, 0, 0, 0.5);
}

.token-gate-title {
    font-size: 16px;
    font-weight: 600;
    color: var(--fg-bright, #e6edf3);
}

.token-gate-hint {
    font-size: 12px;
    line-height: 1.6;
    color: var(--fg-muted, #8b949e);
}

.token-gate-input {
    width: 100%;
    box-sizing: border-box;
    padding: 8px 10px;
    border-radius: 6px;
    border: 1px solid var(--border-tab, #30363d);
    background: var(--bg-input, #0d1117);
    color: var(--fg-bright, #e6edf3);
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    font-size: 13px;
}

.token-gate-input:focus {
    outline: none;
    border-color: var(--accent, #58a6ff);
}

.token-gate-error {
    font-size: 12px;
    color: var(--err, #f85149);
}

.token-gate-btn {
    align-self: flex-end;
    padding: 6px 18px;
    border-radius: 6px;
    border: none;
    background: var(--accent, #2f81f7);
    color: #fff;
    font-size: 13px;
    cursor: pointer;
}

.token-gate-btn:hover {
    filter: brightness(1.1);
}
</style>