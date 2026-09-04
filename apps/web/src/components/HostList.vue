<template>
  <div class="host-list">
    <div class="host-search">
      <input
        v-model="search"
        class="search-input"
        type="text"
        :placeholder="t('host.search')"
        spellcheck="false"
      />
    </div>

    <div v-if="filtered.length === 0" class="host-empty">
      <div class="host-empty-title">{{ t('host.empty') }}</div>
      <div class="host-empty-hint">{{ t('host.emptyHint') }}</div>
    </div>

    <div v-else class="host-scroll">
      <div v-for="g in groups" :key="g.name" class="host-group">
        <button class="group-header" @click="toggleGroup(g.name)">
          <span class="group-caret" :class="{ open: !isCollapsed(g.name) }">▸</span>
          <span class="group-name">{{ g.name || t('host.ungrouped') }}</span>
          <span class="group-count">{{ g.hosts.length }}</span>
        </button>
        <div v-if="!isCollapsed(g.name)" class="group-body">
          <div v-for="h in g.hosts" :key="h.id" class="host-row">
            <div class="host-main">
              <span class="host-name" :title="h.name">{{ h.name }}</span>
              <span class="cred-badge" :class="'cred-' + (h.credential?.kind || 'default')">
                {{ credLabel(h) }}
              </span>
            </div>
            <div class="host-sub">
              <span class="host-addr" :title="h.user + '@' + h.address">{{ h.user }}@{{ h.address }}</span>
              <span v-if="viaChain(h).length" class="host-via" :title="viaChain(h).join(' → ')">
                {{ t('host.via') }} {{ viaChain(h).join(' → ') }}
              </span>
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
      </div>
    </div>

    <div v-if="deleteError" class="host-list-error">{{ deleteError }}</div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
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

const search = ref('')
const collapsedGroups = ref<Set<string>>(new Set())
const confirmingDelete = ref<string | null>(null)

// 主机列表刷新后清除待确认状态(对象已变)
watch(
    () => props.hosts,
    () => {
        confirmingDelete.value = null
    },
)

// 名称/地址/用户过滤(大小写不敏感)
const filtered = computed(() => {
    const q = search.value.trim().toLowerCase()
    if (!q) return props.hosts
    return props.hosts.filter(
        (h) =>
            h.name.toLowerCase().includes(q) ||
            h.address.toLowerCase().includes(q) ||
            h.user.toLowerCase().includes(q),
    )
})

interface HostGroup {
    name: string
    hosts: Host[]
}

// 按 group 分组:有分组名者按字母序,未分组(空)放最后
const groups = computed<HostGroup[]>(() => {
    const byGroup = new Map<string, Host[]>()
    for (const h of filtered.value) {
        const key = h.group || ''
        const list = byGroup.get(key) || []
        list.push(h)
        byGroup.set(key, list)
    }
    const named = [...byGroup.entries()]
        .filter(([k]) => k !== '')
        .sort((a, b) => a[0].localeCompare(b[0]))
    const unnamed = byGroup.get('') || []
    return [...named.map(([name, hosts]) => ({ name, hosts })), ...(unnamed.length ? [{ name: '', hosts: unnamed }] : [])]
})

function isCollapsed(name: string): boolean {
    return collapsedGroups.value.has(name)
}

function toggleGroup(name: string) {
    const next = new Set(collapsedGroups.value)
    if (next.has(name)) next.delete(name)
    else next.add(name)
    collapsedGroups.value = next
}

// via 链:服务端已附 via_names(最外层在前)
function viaChain(h: Host): string[] {
    return h.via_names || []
}

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

.host-search {
    padding: 8px;
    flex: 0 0 auto;
}

.search-input {
    width: 100%;
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

.search-input::placeholder {
    color: var(--fg-hint);
}

.search-input:focus {
    border-color: var(--accent);
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

.group-header {
    display: flex;
    align-items: center;
    gap: 6px;
    width: 100%;
    padding: 6px 10px;
    background: none;
    border: none;
    color: var(--fg-muted);
    font-size: 12px;
    font-family: inherit;
    cursor: pointer;
    text-align: left;
    border-top: 1px solid var(--border-tab);
}

.group-header:hover {
    background: var(--bg-tab-hover);
    color: var(--fg-bright);
}

.group-caret {
    display: inline-block;
    transition: transform 0.15s;
    font-size: 10px;
}

.group-caret.open {
    transform: rotate(90deg);
}

.group-name {
    flex: 1 1 auto;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-weight: 600;
}

.group-count {
    font-weight: 400;
    color: var(--fg-hint);
}

.host-row {
    padding: 6px 10px 6px 22px;
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

.host-via {
    color: var(--fg-hint);
    font-size: 11px;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
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