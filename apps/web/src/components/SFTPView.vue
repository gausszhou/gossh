<template>
  <div class="sftp-view">
    <!-- 工具栏 -->
    <div class="sftp-toolbar">
      <button class="tool-btn" :title="t('sftp.up')" :disabled="!canUp" @click="goUp">↑ {{ t('sftp.up') }}</button>
      <button class="tool-btn" :title="t('sftp.home')" @click="goHome">⌂ {{ t('sftp.home') }}</button>
      <button class="tool-btn" :title="t('sftp.refresh')" :disabled="loading" @click="load(path)">⟳ {{ t('sftp.refresh') }}</button>
      <span class="toolbar-sep"></span>
      <button class="tool-btn" :title="t('sftp.upload')" @click="uploadInput?.click()">↥ {{ t('sftp.upload') }}</button>
      <button class="tool-btn" :title="t('sftp.mkdir')" @click="startAction('mkdir')">＋ {{ t('sftp.mkdir') }}</button>
      <button
        class="tool-btn"
        :title="t('sftp.rename')"
        :disabled="!selected"
        @click="startAction('rename')"
      >✎ {{ t('sftp.rename') }}</button>
      <button
        v-if="confirmingDelete"
        class="tool-btn tool-danger"
        @click="doDelete"
      >{{ t('host.act.confirmDelete') }}</button>
      <button
        v-else
        class="tool-btn tool-danger"
        :title="t('sftp.delete')"
        :disabled="!selected"
        @click="confirmingDelete = true"
      >✕ {{ t('sftp.delete') }}</button>
      <button
        class="tool-btn"
        :title="t('sftp.download')"
        :disabled="!selected || selected.is_dir"
        @click="download(selected)"
      >⇣ {{ t('sftp.download') }}</button>
      <div class="toolbar-spacer"></div>
      <span class="toolbar-status">{{ statusText }}</span>
    </div>

    <!-- 内联输入(新建目录 / 重命名) -->
    <div v-if="action" class="sftp-inline-input">
      <span class="inline-label">{{ actionLabel }}</span>
      <input
        ref="nameInputRef"
        v-model="nameInput"
        class="inline-input"
        type="text"
        :placeholder="action === 'mkdir' ? t('sftp.mkdirPrompt') : selected ? selected.name : ''"
        spellcheck="false"
        @keydown.enter="confirmAction"
        @keydown.esc="cancelAction"
      />
      <button class="tool-btn tool-primary" :disabled="!nameInput.trim()" @click="confirmAction">{{ t('common.confirm') }}</button>
      <button class="tool-btn" @click="cancelAction">{{ t('common.cancel') }}</button>
    </div>

    <!-- 面包屑 -->
    <div class="sftp-crumbs">
      <span class="crumb" @click="go('/')">/</span>
      <template v-for="(seg, i) in crumbs" :key="i">
        <span class="crumb-sep">/</span>
        <span class="crumb" @click="go(crumbsToPath(i))">{{ seg }}</span>
      </template>
    </div>

    <!-- 文件表 -->
    <div class="sftp-table-wrap">
      <table class="sftp-table">
        <thead>
          <tr>
            <th class="col-name">{{ t('sftp.name') }}</th>
            <th class="col-size">{{ t('sftp.size') }}</th>
            <th class="col-time">{{ t('sftp.modTime') }}</th>
            <th class="col-type">{{ t('sftp.type') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-if="loading" class="row-empty"><td colspan="4">{{ t('common.loading') }}</td></tr>
          <tr v-else-if="entries.length === 0" class="row-empty"><td colspan="4">{{ t('sftp.empty') }}</td></tr>
          <tr
            v-for="e in entries"
            :key="e.path"
            class="sftp-row"
            :class="{ selected: selected?.path === e.path }"
            @click="select(e)"
            @dblclick="openEntry(e)"
          >
            <td class="cell-name">
              <span class="dir-icon" :class="{ 'link-icon': e.is_link }">{{ iconFor(e) }}</span>
              <span class="entry-name" :title="e.name">{{ e.name }}</span>
            </td>
            <td class="cell-size">{{ e.is_dir ? '—' : fmtSize(e.size) }}</td>
            <td class="cell-time">{{ fmtTime(e.mod_time) }}</td>
            <td class="cell-type">
              <span class="mode-text" :title="e.mode">{{ typeLabel(e) }}</span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- 错误条 -->
    <div v-if="error" class="sftp-error">{{ error }}</div>

    <!-- 隐藏的上传文件选择 -->
    <input
      ref="uploadInput"
      class="upload-input"
      type="file"
      multiple
      @change="onUpload"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import {
    sftpList, sftpStat, sftpMkdir, sftpRename, sftpRemove, sftpDownload, sftpUpload,
} from '../utils/api'
import { t } from '../utils/i18n'
import { logger } from '../utils/logger'
import type { SftpEntry } from '../utils/types'

const props = defineProps<{
    sessionId: string
    hostName: string
    active?: boolean
}>()

const emit = defineEmits<{
    (e: 'close'): void
    (e: 'title', title: string): void
}>()

// ── 路径状态 ──
// path:传给 API 的原始路径;'' = 家目录(服务端 '.' 即登录目录)
const path = ref('')
const crumbs = computed(() => {
    const p = path.value
    if (!p || p === '/') return []
    return p.replace(/^\.\//, '').replace(/\/$/, '').split('/').filter(Boolean)
})

function crumbsToPath(i: number): string {
    // 面包屑点击:回到第 i 段所在目录(第 0 段 = '/')
    if (i < 0) return '/'
    const segs = crumbs.value.slice(0, i + 1)
    return '/' + segs.join('/')
}

const entries = ref<SftpEntry[]>([])
const selected = ref<SftpEntry | null>(null)
const loading = ref(false)
const error = ref('')
const statusText = ref('')

// ── 加载列表 ──
async function load(p: string) {
    loading.value = true
    error.value = ''
    path.value = p
    selected.value = null
    try {
        entries.value = await sftpList(props.sessionId, p)
    } catch (err) {
        error.value = err instanceof Error ? err.message : String(err)
        entries.value = []
    } finally {
        loading.value = false
    }
}

function go(p: string) {
    if (p !== path.value) void load(p)
}

function goHome() {
    void load('')
}

const canUp = computed(() => {
    const p = path.value
    if (!p || p === '/' || p === '.') return false
    return true
})

function parentOf(p: string): string {
    const t = p.replace(/\/$/, '')
    const idx = t.lastIndexOf('/')
    if (idx <= 0) return '/'
    return t.slice(0, idx)
}

function goUp() {
    if (!canUp.value) return
    void load(parentOf(path.value))
}

// ── 打开条目 ──
function select(e: SftpEntry) {
    selected.value = e
}

async function openEntry(e: SftpEntry) {
    if (e.is_dir) {
        go(e.path)
        return
    }
    if (e.is_link) {
        try {
            const st = await sftpStat(props.sessionId, e.path)
            if (st.is_dir) {
                go(e.path)
                return
            }
        } catch {
            // stat 失败按文件处理
        }
    }
    await download(e)
}

function iconFor(e: SftpEntry): string {
    if (e.is_link) return '↗'
    return e.is_dir ? '▸' : ' '
}

function typeLabel(e: SftpEntry): string {
    if (e.is_dir) return t('sftp.typeDir')
    if (e.is_link) return t('sftp.typeLink')
    return t('sftp.typeFile')
}

// ── 内联动作(mkdir / rename) ──
type InlineAction = 'mkdir' | 'rename' | null
const action = ref<InlineAction>(null)
const nameInput = ref('')
const nameInputRef = ref<HTMLInputElement>()

const actionLabel = computed(() =>
    action.value === 'mkdir' ? t('sftp.mkdirPrompt') : t('sftp.renamePrompt'),
)

function startAction(a: NonNullable<InlineAction>) {
    action.value = a
    nameInput.value = ''
    void nextTick(() => nameInputRef.value?.focus())
}

function cancelAction() {
    action.value = null
    nameInput.value = ''
}

async function confirmAction() {
    const name = nameInput.value.trim()
    if (!name || !action.value) return
    const act = action.value
    try {
        if (act === 'mkdir') {
            const target = joinPath(path.value, name)
            await sftpMkdir(props.sessionId, target)
        } else if (selected.value) {
            const from = selected.value.path
            const to = joinPath(path.value, name)
            if (from !== to) await sftpRename(props.sessionId, from, to)
        }
    } catch (err) {
        error.value = err instanceof Error ? err.message : String(err)
    } finally {
        action.value = null
        nameInput.value = ''
        await load(path.value)
    }
}

function joinPath(dir: string, name: string): string {
    if (!dir || dir === '/') return '/' + name
    return dir.replace(/\/$/, '') + '/' + name
}

// ── 删除(两段式确认) ──
const confirmingDelete = ref(false)

async function doDelete() {
    const e = selected.value
    if (!e) return
    confirmingDelete.value = false
    try {
        await sftpRemove(props.sessionId, e.path)
    } catch (err) {
        error.value = (err instanceof Error ? err.message : String(err)) || t('sftp.opFailed')
    }
    await load(path.value)
}

// ── 下载 ──
async function download(e: SftpEntry | null) {
    if (!e || e.is_dir) return
    try {
        const blob = await sftpDownload(props.sessionId, e.path)
        const url = URL.createObjectURL(blob)
        const a = document.createElement('a')
        a.href = url
        a.download = e.name
        document.body.appendChild(a)
        a.click()
        document.body.removeChild(a)
        setTimeout(() => URL.revokeObjectURL(url), 5000)
    } catch (err) {
        error.value = err instanceof Error ? err.message : String(err)
    }
}

// ── 上传 ──
const uploadInput = ref<HTMLInputElement>()

async function onUpload(ev: Event) {
    const input = ev.target as HTMLInputElement
    const files = input.files ? Array.from(input.files) : []
    input.value = ''
    if (files.length === 0) return
    for (const f of files) {
        statusText.value = t('sftp.uploading').replace('%s', f.name)
        try {
            await sftpUpload(props.sessionId, joinPath(path.value, f.name), f)
            statusText.value = `${f.name} ${t('sftp.uploaded')}`
        } catch (err) {
            error.value = err instanceof Error ? err.message : String(err)
            statusText.value = ''
            break
        }
    }
    await load(path.value)
}

// ── 格式化 ──
function fmtSize(size: number): string {
    if (size < 1024) return `${size} B`
    const units = ['KB', 'MB', 'GB', 'TB']
    let v = size
    let u = -1
    do {
        v /= 1024
        u++
    } while (v >= 1024 && u < units.length - 1)
    return `${v.toFixed(1)} ${units[u]}`
}

function fmtTime(unixSec: number): string {
    if (!unixSec) return '—'
    return new Date(unixSec * 1000).toLocaleString()
}

// ── 生命周期:挂载时加载 / 关闭时静默 ──
watch(
    [() => props.sessionId, () => props.active],
    ([sid, active]) => {
        if (sid && active) void load(path.value || '')
    },
    { immediate: true },
)

onBeforeUnmount(() => {
    logger.info('sftp', 'sftp view closed (session=%s)', props.sessionId)
})
</script>

<style scoped>
.sftp-view {
    display: flex;
    flex-direction: column;
    width: 100%;
    height: 100%;
    min-width: 0;
    min-height: 0;
    background: var(--bg-app);
}

.sftp-toolbar {
    display: flex;
    align-items: center;
    gap: 6px;
    height: 32px;
    flex: 0 0 auto;
    padding: 0 10px;
    background: var(--bg-bar);
    border-bottom: 1px solid var(--bg-bar-border);
    user-select: none;
    flex-wrap: nowrap;
    overflow-x: auto;
}

.tool-btn {
    flex: 0 0 auto;
    background: none;
    border: 1px solid var(--border-tab);
    border-radius: 3px;
    color: var(--fg-dim);
    font-size: 12px;
    font-family: inherit;
    line-height: 1;
    padding: 5px 8px;
    cursor: pointer;
    white-space: nowrap;
}

.tool-btn:hover:not(:disabled) {
    background: var(--bg-tab-hover);
    color: var(--fg-bright);
}

.tool-btn:disabled {
    opacity: 0.4;
    cursor: default;
}

.tool-danger {
    color: var(--net-bad);
}

.tool-primary {
    color: var(--accent);
    border-color: var(--accent);
}

.toolbar-sep {
    width: 1px;
    height: 16px;
    background: var(--border-tab);
    flex: 0 0 auto;
}

.toolbar-spacer {
    flex: 1 1 auto;
}

.toolbar-status {
    flex: 0 0 auto;
    font-size: 12px;
    color: var(--fg-muted);
    white-space: nowrap;
}

/* ── 内联输入行 ── */
.sftp-inline-input {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 10px;
    border-bottom: 1px solid var(--bg-bar-border);
    background: var(--bg-dialog);
    flex: 0 0 auto;
}

.inline-label {
    font-size: 12px;
    color: var(--fg-muted);
    white-space: nowrap;
}

.inline-input {
    flex: 1 1 auto;
    min-width: 0;
    height: 26px;
    padding: 0 8px;
    background: var(--bg-input);
    border: 1px solid var(--border-tab);
    border-radius: 4px;
    color: var(--fg);
    font-size: 12px;
    font-family: inherit;
    outline: none;
}

.inline-input:focus {
    border-color: var(--accent);
}

/* ── 面包屑 ── */
.sftp-crumbs {
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 8px 10px 4px;
    flex: 0 0 auto;
    font-size: 13px;
    color: var(--fg-dim);
    flex-wrap: wrap;
    user-select: none;
}

.crumb {
    cursor: pointer;
    padding: 1px 4px;
    border-radius: 3px;
    color: var(--fg-bright);
}

.crumb:hover {
    background: var(--bg-tab-hover);
}

.crumb-sep {
    color: var(--fg-muted);
}

/* ── 文件表 ── */
.sftp-table-wrap {
    flex: 1 1 auto;
    min-height: 0;
    overflow: auto;
}

.sftp-table {
    width: 100%;
    border-collapse: collapse;
    font-size: 13px;
}

.sftp-table th {
    position: sticky;
    top: 0;
    background: var(--bg-bar);
    color: var(--fg-muted);
    font-weight: 500;
    text-align: left;
    padding: 6px 10px;
    font-size: 12px;
    border-bottom: 1px solid var(--border-tab);
    white-space: nowrap;
    z-index: 1;
}

.sftp-table td {
    padding: 5px 10px;
    border-bottom: 1px solid var(--border-tab);
    color: var(--fg);
    white-space: nowrap;
}

.sftp-row {
    cursor: pointer;
}

.sftp-row:hover {
    background: var(--bg-tab-hover);
}

.sftp-row.selected {
    background: var(--bg-tab-active);
    box-shadow: inset 2px 0 0 var(--accent);
}

.row-empty td {
    text-align: center;
    color: var(--fg-hint);
    padding: 24px 0;
}

.cell-name {
    display: flex;
    align-items: center;
    gap: 6px;
    max-width: 40vw;
    overflow: hidden;
}

.dir-icon {
    color: var(--fg-muted);
    flex: 0 0 auto;
}

.link-icon {
    color: #58a6ff;
}

.entry-name {
    overflow: hidden;
    text-overflow: ellipsis;
}

.cell-size {
    color: var(--fg-dim);
    font-family: 'SF Mono', Consolas, monospace;
    font-size: 12px;
}

.cell-time {
    color: var(--fg-dim);
    font-size: 12px;
}

.cell-type {
    color: var(--fg-muted);
    font-size: 12px;
}

.mode-text {
    font-family: 'SF Mono', Consolas, monospace;
    font-size: 11px;
}

/* ── 错误条 ── */
.sftp-error {
    flex: 0 0 auto;
    padding: 6px 10px;
    color: var(--net-bad);
    font-size: 12px;
    border-top: 1px solid var(--border-tab);
    line-height: 1.5;
    word-break: break-word;
    background: var(--bg-dialog);
}

.upload-input {
    display: none;
}
</style>