<template>
  <div class="tab-bar">
    <!-- 主机列表折叠开关(固定在左侧) -->
    <div class="tab-actions tab-actions-left">
      <button
        class="icon-btn"
        :title="collapsed ? t('host.expand') : t('host.collapse')"
        @click="emit('toggle-sidebar')"
      >{{ collapsed ? '▸' : '◂' }}</button>
    </div>

    <div
      v-for="tab in tabs"
      :key="tab.id"
      class="tab"
      :class="{ active: tab.id === activeId }"
      @click="emit('open', tab.id)"
    >
      <span v-if="tab.kind === 'ssh'" class="state-dot" :class="stateClass(tab)"></span>
      <span v-else class="kind-badge" :class="'kind-' + tab.kind">{{ kindLabel(tab.kind) }}</span>
      <span class="tab-title">{{ tab.title }}</span>
      <button class="tab-close" :title="t('tab.close')" @click.stop="emit('close', tab)">✕</button>
    </div>

    <!-- 右侧工具体栏:新建主机 + 设置 -->
    <div class="tab-actions">
      <button class="icon-btn icon-btn-text" :title="t('tab.newHost')" @click="emit('new-host')">
        ＋ {{ t('tab.newHost') }}
      </button>
      <button class="icon-btn" :title="t('tab.settings')" @click="emit('settings')">⚙</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { t } from '../utils/i18n'
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
    (e: 'settings'): void
    (e: 'new-host'): void
    (e: 'toggle-sidebar'): void
}>()

// sftp/run 页签的小徽标(ssh 用状态圆点,不用徽标)
function kindLabel(kind: AppTab['kind']): string {
    if (kind === 'sftp') return t('sftp.tabSuffix')
    return t('run.tabSuffix')
}

function stateClass(tab: AppTab): string {
    if (tab.connected) return 'dot-running'
    if (tab.alive === false) return 'dot-dead'
    if (tab.alive) return 'dot-idle'
    return 'dot-idle'
}
</script>

<style scoped>
.tab-bar {
    display: flex;
    align-items: stretch;
    height: 32px;
    flex: 0 0 auto;
    background: var(--bg-bar);
    border-bottom: 1px solid var(--bg-bar-border);
    overflow-x: auto;
    overflow-y: hidden;
    position: relative;
    user-select: none;
}

.tab {
    display: flex;
    align-items: center;
    gap: 6px;
    max-width: 220px;
    min-width: 100px;
    padding: 0 8px;
    border-right: 1px solid var(--bg-bar-border);
    color: var(--fg-dim);
    font-size: 13px;
    cursor: pointer;
    white-space: nowrap;
    flex: 0 0 auto;
    background: var(--bg-tab);
    border-top: 1px solid var(--border-tab);
}

.tab.active {
    background: var(--bg-tab-active);
    color: var(--fg-bright);
    border-top: 1px solid var(--accent); /* VSCode 活动页签顶条 */
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

/* sftp / run 页签的类型徽标 */
.kind-badge {
    flex: 0 0 auto;
    font-size: 10px;
    line-height: 1;
    padding: 2px 4px;
    border-radius: 3px;
    border: 1px solid var(--border-tab);
    color: var(--fg-muted);
}

.kind-sftp {
    color: #58a6ff;
}

.kind-run {
    color: #d29922;
}

.tab-close {
    background: none;
    border: none;
    color: var(--fg-dim);
    font-size: 11px;
    line-height: 1;
    padding: 2px 4px;
    border-radius: 3px;
    cursor: pointer;
    flex: 0 0 auto;
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

/* 折叠开关固定在左侧,滚动时保持可见 */
.tab-actions-left {
    margin-left: 0;
    position: sticky;
    left: 0;
    z-index: 2;
}

.icon-btn {
    background: none;
    border: none;
    color: var(--fg);
    font-size: 14px;
    cursor: pointer;
    padding: 2px 8px;
    line-height: 1;
    border-radius: 3px;
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