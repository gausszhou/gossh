<template>
  <div class="app">
    <!-- 左侧:主机列表栏(可折叠) -->
    <aside class="sidebar" :class="{ collapsed: sidebarCollapsed }">
      <div class="sidebar-inner">
        <HostList
          :hosts="hosts"
          @connect="connectHost"
          @run="openRunModal"
          @sftp="sftpHost"
          @edit="editHost"
          @delete="refreshHosts"
        />
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
          <SFTPView
            v-else-if="tab.kind === 'sftp'"
            v-show="tab.id === activeTabId"
            :ref="(el) => setViewRef(tab.id, el)"
            :session-id="tab.sessionId!"
            :host-name="tab.hostName || ''"
            :active="tab.id === activeTabId"
            @close="closeTab(tab)"
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
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref } from 'vue'
import TabBar from './components/TabBar.vue'
import HostList from './components/HostList.vue'
import HostFormModal from './components/HostFormModal.vue'
import RunModal from './components/RunModal.vue'
import CredentialsModal from './components/CredentialsModal.vue'
import ForwardModal from './components/ForwardModal.vue'
import SettingsModal from './components/SettingsModal.vue'
import SshView from './components/SshView.vue'
import SFTPView from './components/SFTPView.vue'
import RunView from './components/RunView.vue'
import {
    listHosts, createSession, checkSessions, destroySession, updateSessionTitle,
    isCredentialError, getPageTitle,
} from './utils/api'
import { applyTheme, currentTheme, notifyThemeChange, type Theme } from './utils/theme'
import { t } from './utils/i18n'
import {
    loadManifest, upsertManifest, removeFromManifest, generateSessionID, type ManifestEntry,
} from './utils/manifest'
import { logger } from './utils/logger'
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
    document.title = title || 'gossh'
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
    if (activate) activeTabId.value = tab.id
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
async function sftpHost(host: Host) {
    logger.info('app', 'open sftp host=%s (%s)', host.id, host.name)
    const existing = await findAliveSessionForHost(host.id)
    if (existing) {
        addSftpTab(existing, host)
        return
    }
    const id = generateSessionID()
    try {
        const s = await createSession({ host_id: host.id, id })
        addSftpTab(s.id, host)
    } catch (err) {
        if (isCredentialError(err)) {
            openCredPrompt('connect', host.id, id, host.name, (s) => addSftpTab(s.id, host))
            return
        }
        showToast(err instanceof Error ? err.message : String(err))
    }
}

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

function addSftpTab(sessionId: string, host: Host) {
    const id = `sftp-${++tabSeq}`
    pushTab({
        id,
        kind: 'sftp',
        title: `${host.name} · ${t('sftp.tabSuffix')}`,
        sessionId,
        hostId: host.id,
        hostName: host.name,
        createdAt: Date.now(),
    })
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

// ── 启动 ──
const booting = ref(true)
const bootError = ref('')

onMounted(async () => {
    document.title = 'gossh'

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
</style>