<template>
  <div class="host-list">
    <!-- 侧栏头部:分组标题 + 宽样式「新建主机」按钮
         (展开/收起按钮在页签栏最左侧,见 TabBar) -->
    <div class="host-header">
      <span class="host-header-title">{{ t('host.title') }}</span>
      <button class="new-host-btn" :title="t('tab.newHost')" @click="emit('new-host')">
        <Plus :size="14" /><span class="new-host-label">{{ t('tab.newHost') }}</span>
      </button>
    </div>

    <div v-if="hosts.length === 0" class="host-empty">
      <Server :size="26" class="host-empty-icon" />
      <div class="host-empty-title">{{ t('host.empty') }}</div>
      <div class="host-empty-hint">{{ t('host.emptyHint') }}</div>
    </div>

    <div v-else class="host-scroll">
      <div
        v-for="h in hosts"
        :key="h.id"
        class="host-row"
        :class="{ 'menu-open': menuHost?.id === h.id }"
        :title="t('host.rowHint')"
        @contextmenu.prevent.stop="openMenu($event, h)"
      >
        <!-- 两行布局:第一行名称,第二行 user@addr:port(悬停时让位给操作按钮) -->
        <div class="host-line1">
          <span class="host-name">{{ h.name }}</span>
        </div>
        <div class="host-line2">
          <span class="host-addr">{{ h.user }}@{{ h.address }}<template v-if="h.port && h.port !== 22">:{{ h.port }}</template></span>
        </div>

        <!-- 行内操作(第二行右侧):悬停/聚焦时出现;两段式删除确认 -->
        <span class="row-actions">
          <template v-if="confirmingDelete === h.id">
            <button class="row-btn danger" :title="t('host.act.confirmDelete')" @click.stop="doDelete(h)">
              <Check :size="13" />
            </button>
            <button class="row-btn" :title="t('common.cancel')" @click.stop="confirmingDelete = null">
              <X :size="13" />
            </button>
          </template>
          <template v-else>
            <!-- 连接:唯一的建会话入口(点击行本身不建连,避免误触) -->
            <button class="row-btn connect" :title="t('host.act.connect')" @click.stop="emit('connect', h)">
              <Play :size="14" />
            </button>
            <button class="row-btn" :title="t('host.act.forwards')" @click.stop="emit('forwards', h)">
              <ArrowLeftRight :size="13" />
            </button>
            <button class="row-btn" :title="t('host.act.edit')" @click.stop="emit('edit', h)">
              <Pencil :size="13" />
            </button>
            <button class="row-btn danger" :title="t('host.act.delete')" @click.stop="armDelete(h)">
              <Trash2 :size="13" />
            </button>
            <button class="row-btn" :title="t('host.act.more')" @click.stop="openMenu($event, h)">
              <EllipsisVertical :size="14" />
            </button>
          </template>
        </span>
      </div>
    </div>

    <div v-if="deleteError" class="host-list-error">{{ deleteError }}</div>

    <!-- 右键状态菜单(Teleport 到 body:不受侧栏 overflow 裁剪) -->
    <Teleport to="body">
      <div v-if="menuHost" class="host-menu-layer" @mousedown.self="closeMenu" @contextmenu.prevent>
        <div class="host-menu" :style="{ left: menuX + 'px', top: menuY + 'px' }">
          <div class="host-menu-head">{{ menuHost.name }}</div>
          <button class="host-menu-item" @click="menuAction('connect')">
            <TerminalIcon :size="14" /><span>{{ t('host.act.connect') }}</span>
          </button>
          <button class="host-menu-item" @click="menuAction('forwards')">
            <ArrowLeftRight :size="14" /><span>{{ t('host.act.forwards') }}</span>
          </button>
          <button class="host-menu-item" @click="menuAction('edit')">
            <Pencil :size="14" /><span>{{ t('host.act.edit') }}</span>
          </button>
          <div class="host-menu-sep"></div>
          <button class="host-menu-item danger" @click="menuAction('delete')">
            <Trash2 :size="14" /><span>{{ t('host.act.delete') }}</span>
          </button>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from 'vue'
import {
    ArrowLeftRight, Check, EllipsisVertical, Pencil, Play, Plus, Server,
    Terminal as TerminalIcon, Trash2, X,
} from 'lucide-vue-next'
import { deleteHost } from '../utils/api'
import { t } from '../utils/i18n'
import { logger } from '../utils/logger'
import type { Host } from '../utils/types'

const props = defineProps<{
    hosts: Host[]
}>()

const emit = defineEmits<{
    (e: 'connect', host: Host): void
    (e: 'forwards', host: Host): void
    (e: 'edit', host: Host): void
    // 已确认删除(列表内两段式确认);App 调 API 后刷新
    (e: 'delete', host: Host): void
    // 新建主机(打开 HostFormModal,App 侧 openHostForm(null))
    (e: 'new-host'): void
}>()

const confirmingDelete = ref<string | null>(null)

// 主机列表刷新后清除待确认状态(对象已变)
watch(
    () => props.hosts,
    () => {
        confirmingDelete.value = null
    },
)

function armDelete(h: Host) {
    confirmingDelete.value = h.id
}

// 删除:两段式确认后直接调 API(App 收到事件后刷新列表)
async function doDelete(h: Host) {
    confirmingDelete.value = null
    try {
        await deleteHost(h.id)
        logger.info('host', 'deleted host=%s (%s)', h.id, h.name)
        emit('delete', h)
    } catch (err) {
        logger.warn('host', 'failed to delete host=%s: %s', h.id, err)
        deleteError.value = err instanceof Error ? err.message : String(err)
        showDeleteError()
    }
}

// 删除失败的轻提示(内联于列表底部,3s 后消失)
const deleteError = ref('')
let deleteErrorTimer: ReturnType<typeof setTimeout> | null = null
function showDeleteError() {
    if (deleteErrorTimer) clearTimeout(deleteErrorTimer)
    deleteErrorTimer = setTimeout(() => {
        deleteError.value = ''
    }, 3000)
}

// ── 右键 / ⋮ 菜单(与 VS Code 资源管理器右键菜单同款) ──
const menuHost = ref<Host | null>(null)
const menuX = ref(0)
const menuY = ref(0)

function openMenu(e: MouseEvent, h: Host) {
    menuHost.value = h
    // 贴边时向左/向上收,避免菜单溢出视口
    menuX.value = Math.min(e.clientX, window.innerWidth - 200)
    menuY.value = Math.min(e.clientY, window.innerHeight - 180)
    logger.info('host', 'open host menu host=%s', h.id)
}

function closeMenu() {
    menuHost.value = null
}

function onKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') closeMenu()
}

watch(menuHost, (v) => {
    if (!v) {
        window.removeEventListener('keydown', onKeydown)
        return
    }
    // 下一帧再挂全局监听:避免触发本次打开的同一事件立刻关闭菜单
    void nextTick(() => window.addEventListener('keydown', onKeydown))
})

function menuAction(kind: 'connect' | 'forwards' | 'edit' | 'delete') {
    const h = menuHost.value
    closeMenu()
    if (!h) return
    // 逐个分支 emit:重载签名需要字面量事件名,不能用联合类型直接透传
    if (kind === 'delete') {
        armDelete(h)
    } else if (kind === 'connect') {
        emit('connect', h)
    } else if (kind === 'forwards') {
        emit('forwards', h)
    } else {
        emit('edit', h)
    }
}

onBeforeUnmount(() => {
    window.removeEventListener('keydown', onKeydown)
    if (deleteErrorTimer) clearTimeout(deleteErrorTimer)
})
</script>

<style scoped>
.host-list {
    display: flex;
    flex-direction: column;
    height: 100%;
    min-width: 0;
    background: var(--bg-bar);
}

/* ── 侧栏头部(分组标题 + 宽样式「新建主机」按钮) ── */
.host-header {
    flex: 0 0 auto;
    display: flex;
    flex-direction: column;
    gap: 6px;
    padding: 8px 8px 6px;
    user-select: none;
}

.host-header-title {
    min-width: 0;
    padding-left: 2px;
    font-size: 11px;
    font-weight: 400;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--fg-muted);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
}

/* 宽样式:横向铺满侧栏(上限 320px,VSCode 欢迎页按钮观感) */
.new-host-btn {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 6px;
    width: 100%;
    max-width: 320px;
    height: 28px;
    margin: 0 auto;
    padding: 0 10px;
    background: none;
    border: 1px solid var(--border-input);
    border-radius: var(--radius-md);
    color: var(--fg);
    font-size: 13px;
    font-family: inherit;
    line-height: 1;
    cursor: pointer;
    white-space: nowrap;
    overflow: hidden;
}

.new-host-label {
    overflow: hidden;
    text-overflow: ellipsis;
}

.new-host-btn:hover {
    background: var(--bg-tab-hover);
    border-color: var(--focus-border);
    color: var(--fg-bright);
}

/* ── 空态 ── */
.host-empty {
    flex: 1 1 auto;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 6px;
    padding: 24px 16px;
    text-align: center;
    color: var(--fg-hint);
}

.host-empty-icon {
    color: var(--fg-hint);
    opacity: 0.7;
}

.host-empty-title {
    font-size: 13px;
    color: var(--fg-dim);
}

.host-empty-hint {
    font-size: 12px;
    line-height: 1.6;
}

/* ── 列表(主机行两行布局:名称+凭据 / 地址) ── */
.host-scroll {
    flex: 1 1 auto;
    overflow-y: auto;
    overflow-x: hidden;
    min-height: 0;
    padding: 2px 0;
}

.host-row {
    position: relative;
    display: flex;
    flex-direction: column;
    justify-content: center;
    gap: 1px;
    padding: 4px 6px 4px 8px;
    cursor: default;
    color: var(--fg-dim);
    font-size: 13px;
    white-space: nowrap;
    border-radius: var(--radius-sm);
}

.host-row:hover,
.host-row.menu-open {
    background: var(--bg-tab-hover);
    color: var(--fg-bright);
}

.host-row:hover .host-name,
.host-row.menu-open .host-name {
    color: var(--fg-bright);
}

/* 第一行:名称 */
.host-line1 {
    display: flex;
    align-items: center;
    gap: 6px;
    min-width: 0;
}

/* 第二行:user@addr:port(悬停时隐藏,让位给右侧操作按钮) */
.host-line2 {
    display: flex;
    align-items: center;
    min-width: 0;
    padding-right: 2px;
}

.host-name {
    flex: 0 1 auto;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    color: var(--fg);
}

.host-addr {
    flex: 0 1 auto;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    font-family: 'SF Mono', Consolas, 'DejaVu Sans Mono', monospace;
    font-size: 11px;
    color: var(--fg-hint);
}

.host-row:hover .host-addr {
    visibility: hidden;
}

/* ── 行内操作(第二行右侧,悬停 / 键盘聚焦时出现) ── */
.row-actions {
    position: absolute;
    right: 4px;
    bottom: 4px;
    display: none;
    align-items: center;
    gap: 1px;
}

.host-row:hover .row-actions,
.host-row:focus-within .row-actions {
    display: flex;
}

.row-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 20px;
    height: 20px;
    padding: 0;
    background: none;
    border: none;
    border-radius: var(--radius-md);
    color: var(--fg-dim);
    cursor: pointer;
}

.row-btn:hover {
    background: var(--list-active);
    color: var(--fg-bright);
}

.row-btn.danger:hover {
    background: color-mix(in srgb, var(--net-bad) 30%, transparent);
    color: var(--fg-bright);
}

/* 连接:唯一建会话入口,用绿色实心图标凸显(VSCode 运行按钮观感) */
.row-btn.connect {
    color: var(--net-good);
    margin-right: 2px;
}

.row-btn.connect:hover {
    background: color-mix(in srgb, var(--net-good) 28%, transparent);
    color: var(--fg-bright);
}

.host-list-error {
    flex: 0 0 auto;
    padding: 6px 10px;
    color: var(--net-bad);
    font-size: 12px;
}

/* ── 右键菜单 ── */
.host-menu-layer {
    position: fixed;
    inset: 0;
    z-index: 1500;
}

.host-menu {
    position: fixed;
    min-width: 168px;
    padding: 4px;
    background: var(--bg-dialog);
    border: 1px solid var(--border-dialog);
    border-radius: var(--radius-md);
    box-shadow: var(--shadow-widget);
    display: flex;
    flex-direction: column;
    gap: 1px;
}

.host-menu-head {
    padding: 3px 8px 5px;
    font-size: 11px;
    color: var(--fg-hint);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.host-menu-item {
    display: flex;
    align-items: center;
    gap: 8px;
    height: 24px;
    padding: 0 8px;
    background: none;
    border: none;
    border-radius: var(--radius-sm);
    color: var(--fg);
    font-size: 13px;
    font-family: inherit;
    text-align: left;
    cursor: pointer;
}

.host-menu-item:hover {
    background: var(--list-active);
    color: var(--fg-bright);
}

.host-menu-item.danger:hover {
    background: color-mix(in srgb, var(--net-bad) 35%, transparent);
}

.host-menu-sep {
    height: 1px;
    margin: 3px 6px;
    background: var(--border-dialog);
}
</style>
