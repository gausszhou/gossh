<template>
  <div ref="barEl" class="tab-bar" @dragover="onBarDragOver" @drop.prevent="onDrop">
    <!-- 主机列表展开/收起(固定在页签栏最左侧,滚动时保持可见) -->
    <div class="tab-actions tab-actions-left">
      <button
        class="icon-btn"
        :title="collapsed ? t('host.expand') : t('host.collapse')"
        @click="emit('toggle-sidebar')"
      >
        <PanelLeftOpen v-if="collapsed" :size="15" /><PanelLeftClose v-else :size="15" />
      </button>
    </div>

    <div
      v-for="(tab, index) in tabs"
      :key="tab.id"
      class="tab"
      :data-tab-id="tab.id"
      :class="[
        { active: tab.id === activeId },
        { dragging: dragId === tab.id },
        { 'drop-left': dropIndex === index },
        { 'drop-right': dropIndex === index + 1 && dropIndex === tabs.length },
      ]"
      draggable="true"
      :title="t('tab.dragHint')"
      @click="openTab(tab.id)"
      @dragstart="onDragStart($event, tab.id)"
      @dragend="onDragEnd"
    >
      <span class="state-dot" :class="stateClass(tab)"></span>
      <span class="tab-title">{{ tab.title }}</span>
      <button class="tab-close" :title="t('tab.close')" @click.stop="emit('close', tab)">
        <X :size="13" />
      </button>
    </div>

    <!-- 右侧工具体栏:设置(新建主机已迁至左侧主机列表) -->
    <div class="tab-actions">
      <button class="icon-btn" :title="t('tab.settings')" @click="emit('settings')">
        <Settings :size="15" />
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onBeforeUnmount } from 'vue'
import { PanelLeftClose, PanelLeftOpen, Settings, X } from 'lucide-vue-next'
import { t } from '../utils/i18n'
import { logger } from '../utils/logger'
import type { AppTab } from '../utils/types'

const props = defineProps<{
    tabs: AppTab[]
    activeId?: string
    // 主机列表是否已折叠(切换按钮的方向提示)
    collapsed?: boolean
}>()

const emit = defineEmits<{
    (e: 'open', tabId: string): void
    (e: 'close', tab: AppTab): void
    (e: 'reorder', from: number, to: number): void
    (e: 'settings'): void
    (e: 'toggle-sidebar'): void
}>()

function stateClass(tab: AppTab): string {
    if (tab.connected) return 'dot-running'
    if (tab.alive === false) return 'dot-dead'
    return 'dot-idle'
}

// ── 拖拽排序 ──
// dragId:正在拖拽的页签 id;dropIndex:当前悬停插入位置(0..tabs.length)。
// drag 结束后浏览器会在原处补发 click,用时间窗吞掉它,避免误切换页签。
// 排序本身由 App.vue 负责(emit('reorder', from, to) + 持久化到
// gossh.tabOrder);本组件只负责拖拽手势与插入位置指示。
const dragId = ref<string | null>(null)
const dropIndex = ref<number | null>(null)
let suppressClickUntil = 0

// 页签栏元素(dragover 冒泡到容器统一计算插入位,避免逐页签绑定)。
const barEl = ref<HTMLElement | null>(null)

function onDragStart(e: DragEvent, id: string) {
    // 从关闭按钮发起的"拖动"不视为页签拖拽:取消本次 dragstart,
    // 点击关闭仍走 click(已在关闭按钮上 @click.stop)。
    if ((e.target as HTMLElement).closest('.tab-close')) {
        e.preventDefault()
        return
    }
    if (!e.dataTransfer) return
    dragId.value = id
    e.dataTransfer.effectAllowed = 'move'
    // Firefox 要求 dragstart 中写入 data,否则不进入拖拽
    e.dataTransfer.setData('text/plain', id)
    logger.info('tab', 'drag start tab=%s', id)
}

// 容器统一 dragover:计算鼠标所在的插入位(0..n),仅活动拖拽时响应
function onBarDragOver(e: DragEvent) {
    if (dragId.value === null || !e.dataTransfer) return
    const target = e.target as HTMLElement
    // 左右操作区(折叠开关/新建/设置)不参与拖拽落点
    if (target.closest('.tab-actions')) return
    e.preventDefault()
    e.dataTransfer.dropEffect = 'move'
    const bar = barEl.value
    const items = bar ? Array.from(bar.querySelectorAll('.tab')) : []
    const x = e.clientX
    let idx = items.length
    for (let i = 0; i < items.length; i++) {
        const rect = items[i].getBoundingClientRect()
        if (x < rect.left) {
            idx = i
            break
        }
        if (x <= rect.right) {
            idx = x < rect.left + rect.width / 2 ? i : i + 1
            break
        }
    }
    dropIndex.value = idx
    // 边缘自动滚动:接近左右边界时滚动页签栏,露出更多页签
    if (bar && x < bar.getBoundingClientRect().left + 20) {
        bar.scrollLeft -= 12
    } else if (bar && x > bar.getBoundingClientRect().right - 20) {
        bar.scrollLeft += 12
    }
}

// 落点:把拖拽页签移动到 dropIndex 处,交给 App.vue 重排并持久化
function onDrop(e: DragEvent) {
    e.preventDefault()
    const id = dragId.value
    const to = dropIndex.value
    // drop 后浏览器会补发 click,时间窗内吞掉,避免误切换页签
    suppressClickUntil = Date.now() + 350
    if (id === null || to === null) return resetDrag()
    const from = props.tabs.findIndex((d) => d.id === id)
    if (from === -1 || from === to) return resetDrag() // 无效/原位放下,不变
    emit('reorder', from, to)
    logger.info('tab', 'dragged tab=%s %d -> %d', id, from, to)
    resetDrag()
}

function onDragEnd() {
    // 拖拽取消/完成都清理状态;dragend 后同样可能补发 click
    suppressClickUntil = Date.now() + 350
    resetDrag()
}

function resetDrag() {
    dragId.value = null
    dropIndex.value = null
}

// openTab 吞掉拖拽结束后的补发 click(350ms 时间窗)
function openTab(id: string) {
    if (Date.now() < suppressClickUntil) return
    emit('open', id)
}

onBeforeUnmount(() => {
    dragId.value = null
    dropIndex.value = null
})
</script>

<style scoped>
.tab-bar {
    display: flex;
    align-items: stretch;
    height: var(--bar-height);
    flex: 0 0 auto;
    background: var(--bg-bar);
    border-bottom: 1px solid var(--bg-bar-border);
    overflow-x: auto;
    overflow-y: hidden;
    position: relative;
    user-select: none;
    scrollbar-width: none; /* 页签栏不显示横向滚动条 */
}

.tab-bar::-webkit-scrollbar {
    height: 0;
}

.tab {
    display: flex;
    align-items: center;
    gap: 5px;
    max-width: 240px;
    min-width: 0;
    padding: 0 8px 0 10px;
    border-right: 1px solid var(--border-tab);
    color: var(--fg-dim);
    font-size: 13px;
    cursor: grab;
    white-space: nowrap;
    flex: 0 0 auto;
    background: var(--bg-tab);
    position: relative;
}

/* VSCode 活动页签:面与编辑器一致,顶部 1px 强调条 */
.tab.active {
    background: var(--bg-tab-active);
    color: var(--fg-bright);
}

.tab.active::before {
    content: '';
    position: absolute;
    top: 0;
    left: 0;
    right: 0;
    height: 1px;
    background: var(--accent);
}

.tab:hover:not(.active) {
    background: color-mix(in srgb, var(--bg-tab-hover) 55%, var(--bg-tab));
}

/* 拖拽中的页签:半透明 + 抓取指针 */
.tab.dragging {
    opacity: 0.45;
    cursor: grabbing;
}

/* ── 拖拽插入指示:在插入位置的页签边缘画一条 2px 强调线 ── */
.tab.drop-left {
    box-shadow: inset 2px 0 0 var(--accent);
    cursor: grabbing;
}

.tab.drop-right {
    box-shadow: inset -2px 0 0 var(--accent);
    cursor: grabbing;
}

.tab-title {
    flex: 1 1 auto;
    overflow: hidden;
    text-overflow: ellipsis;
    min-width: 0;
}

.state-dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    flex: 0 0 auto;
}

.dot-idle {
    background: var(--dot-idle);
}

.dot-running {
    background: var(--dot-running);
}

.dot-dead {
    background: var(--dot-dead);
}

.tab-close {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 18px;
    height: 18px;
    background: none;
    border: none;
    color: var(--fg-dim);
    line-height: 1;
    padding: 0;
    border-radius: var(--radius-md);
    cursor: pointer;
    flex: 0 0 auto;
    opacity: 0;
}

.tab:hover .tab-close,
.tab.active .tab-close,
.tab-close:focus-visible {
    opacity: 1;
}

.tab-close:hover {
    background: var(--bg-tab-hover);
    color: var(--fg-bright);
}

.tab-actions {
    display: flex;
    align-items: center;
    gap: 2px;
    margin-left: auto;
    padding: 0 6px;
    flex: 0 0 auto;
    position: sticky;
    right: 0;
    background: var(--bg-bar);
}

/* 折叠态:展开入口固定在页签栏最左,滚动时保持可见 */
.tab-actions-left {
    margin-left: 0;
    margin-right: 4px;
    position: sticky;
    left: 0;
    z-index: 2;
    padding: 0 2px 0 4px;
}

.icon-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 26px;
    height: 26px;
    background: none;
    border: none;
    border-radius: var(--radius-md);
    color: var(--fg);
    cursor: pointer;
}

.icon-btn:hover {
    background: var(--bg-tab-hover);
    color: var(--fg-bright);
}

.icon-btn-text {
    font-size: 12px;
    white-space: nowrap;
}
</style>