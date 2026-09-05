<template>
  <div class="host-list">
    <div v-if="hosts.length === 0" class="host-empty">
      <div class="host-empty-title">{{ t('host.empty') }}</div>
      <div class="host-empty-hint">{{ t('host.emptyHint') }}</div>
    </div>

    <div v-else class="host-scroll">
      <div v-for="h in hosts" :key="h.id" class="host-row">
        <div class="host-main">
          <span class="host-name" :title="h.name">{{ h.name }}</span>
          <span class="cred-badge" :class="'cred-' + (h.credential?.kind || 'default')">
            {{ credLabel(h) }}
          </span>
        </div>
        <div class="host-sub">
          <span class="host-addr" :title="h.user + '@' + h.address">{{ h.user }}@{{ h.address }}</span>
        </div>
        <div class="host-actions">
          <button class="act-btn act-primary" :title="t('host.act.connect')" @click="emit('connect', h)">
            {{ t('host.act.connect') }}
          </button>
          <button class="act-btn" :title="t('host.act.forwards')" @click="emit('forwards', h)">
            ⇄ {{ t('host.act.forwards') }}
          </button>
          <button class="act-btn" :title="t('host.act.edit')" @click="emit('edit', h)">
            {{ t('host.act.edit') }}
          </button>
          <button
            v-if="confirmingDelete !== h.id"
            class="act-btn act-danger"
            :title="t('host.act.delete')"
            @click="confirmingDelete = h.id"
          >{{ t('host.act.delete') }}</button>
          <span v-else class="delete-confirm">
            <button class="act-btn act-danger act-confirm" @click="doDelete(h)">{{ t('host.act.confirmDelete') }}</button>
            <button class="act-btn" @click="confirmingDelete = null">{{ t('common.cancel') }}</button>
          </span>
        </div>
      </div>
    </div>

    <div v-if="deleteError" class="host-list-error">{{ deleteError }}</div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
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
}>()

const confirmingDelete = ref<string | null>(null)

// 主机列表刷新后清除待确认状态(对象已变)
watch(
    () => props.hosts,
    () => {
        confirmingDelete.value = null
    },
)

// 凭据徽标文案:key 显示 key_path 后缀
function credLabel(h: Host): string {
    const kind = h.credential?.kind || 'default'
    if (kind === 'key' && h.credential.key_path) {
        const parts = h.credential.key_path.split('/')
        return `${kind}(${parts[parts.length - 1]})`
    }
    return kind
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
</script>

<style scoped>
.host-list {
    display: flex;
    flex-direction: column;
    height: 100%;
    min-width: 0;
}

.host-scroll {
    flex: 1 1 auto;
    overflow-y: auto;
    min-height: 0;
}

.host-empty {
    padding: 32px 16px;
    text-align: center;
    color: var(--fg-hint);
}

.host-empty-title {
    font-size: 13px;
    color: var(--fg-dim);
    margin-bottom: 6px;
}

.host-empty-hint {
    font-size: 12px;
    line-height: 1.6;
}

.host-row {
    padding: 6px 10px;
    border-top: 1px solid var(--border-tab);
    cursor: default;
}

.host-row:hover {
    background: var(--bg-tab-hover);
}

.host-main {
    display: flex;
    align-items: center;
    gap: 6px;
}

.host-name {
    font-size: 13px;
    color: var(--fg-bright);
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.cred-badge {
    flex: 0 0 auto;
    font-size: 10px;
    line-height: 1;
    padding: 2px 4px;
    border-radius: 3px;
    border: 1px solid var(--border-tab);
    color: var(--fg-muted);
    max-width: 110px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.cred-default {
    color: var(--fg-muted);
}

.cred-key {
    color: #d29922;
}

.cred-agent {
    color: #58a6ff;
}

.cred-password {
    color: #3fb950;
}

.host-sub {
    display: flex;
    align-items: center;
    gap: 6px;
    margin-top: 2px;
    font-size: 12px;
    color: var(--fg-dim);
}

.host-addr {
    font-family: 'SF Mono', Consolas, monospace;
    font-size: 11px;
}

.host-actions {
    display: flex;
    align-items: center;
    gap: 2px;
    margin-top: 5px;
    flex-wrap: wrap;
}

.act-btn {
    background: none;
    border: 1px solid var(--border-tab);
    border-radius: 3px;
    color: var(--fg-dim);
    font-size: 11px;
    font-family: inherit;
    line-height: 1;
    padding: 3px 6px;
    cursor: pointer;
}

.act-btn:hover {
    background: var(--bg-tab-hover);
    color: var(--fg-bright);
}

.act-primary {
    color: var(--accent);
    border-color: var(--accent);
}

.act-danger {
    color: var(--net-bad);
    border-color: var(--net-bad);
}

.act-confirm {
    background: rgba(248, 81, 73, 0.15);
}

.delete-confirm {
    display: inline-flex;
    gap: 2px;
}

.host-list-error {
    padding: 6px 10px;
    color: var(--net-bad);
    font-size: 12px;
}
</style>