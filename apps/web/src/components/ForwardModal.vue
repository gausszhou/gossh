<template>
  <Teleport to="body">
    <div
      v-if="open"
      class="fwd-overlay"
      role="dialog"
      aria-modal="true"
      @mousedown.self="close"
      @keydown.esc.window="close"
    >
      <div class="fwd-dialog">
        <div class="fwd-header">
          <span class="fwd-title">{{ t('fwd.title') }}</span>
          <button class="fwd-close" :title="t('common.close')" @click="close">✕</button>
        </div>

        <div class="fwd-body">
          <!-- 新增表单 -->
          <div class="fwd-form">
            <div class="fwd-row">
              <label class="fwd-field fwd-kind-field">
                <span class="fwd-label">{{ t('fwd.kind') }}</span>
                <select v-model="kind" class="fwd-input">
                  <option value="local">{{ t('fwd.local') }}</option>
                  <option value="remote">{{ t('fwd.remote') }}</option>
                  <option value="dynamic">{{ t('fwd.dynamic') }}</option>
                </select>
              </label>
              <label class="fwd-field">
                <span class="fwd-label">{{ t('fwd.bind') }}</span>
                <input
                  v-model="bind"
                  class="fwd-input"
                  type="text"
                  :placeholder="t('fwd.bindPlaceholder')"
                  spellcheck="false"
                  @keydown.enter="add"
                />
              </label>
              <label class="fwd-field">
                <span class="fwd-label">{{ t('fwd.target') }}</span>
                <input
                  v-model="target"
                  class="fwd-input"
                  type="text"
                  :placeholder="t('fwd.targetPlaceholder')"
                  spellcheck="false"
                  :disabled="kind === 'dynamic'"
                  @keydown.enter="add"
                />
              </label>
            </div>
            <div class="fwd-add-row">
              <button class="btn-primary" :disabled="busy" @click="add">{{ t('fwd.add') }}</button>
            </div>
          </div>

          <div v-if="error" class="fwd-error">{{ error }}</div>

          <!-- 现有转发列表 -->
          <div class="fwd-list-title">{{ t('fwd.listTitle') }}</div>
          <div v-if="forwards.length === 0" class="fwd-empty">{{ t('fwd.empty') }}</div>
          <div v-else class="fwd-list">
            <div v-for="f in forwards" :key="f.id" class="fwd-item">
              <span class="fwd-item-kind" :class="'kind-' + f.kind">{{ f.kind }}</span>
              <span class="fwd-item-spec">{{ f.bind }}<template v-if="f.target"> → {{ f.target }}</template></span>
              <button class="fwd-item-del" :title="t('common.delete')" @click="remove(f.id)">✕</button>
            </div>
          </div>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { listForwards, addForward, deleteForward } from '../utils/api'
import { t } from '../utils/i18n'
import { logger } from '../utils/logger'
import type { ForwardEntry } from '../utils/types'

const props = defineProps<{
    open: boolean
    sessionId: string
}>()

const emit = defineEmits<{
    (e: 'close'): void
}>()

const forwards = ref<ForwardEntry[]>([])
const kind = ref<'local' | 'remote' | 'dynamic'>('local')
const bind = ref('')
const target = ref('')
const busy = ref(false)
const error = ref('')

// 打开时刷新列表
watch(
    () => props.open,
    (open) => {
        if (!open) return
        error.value = ''
        bind.value = ''
        target.value = ''
        kind.value = 'local'
        void refresh()
    },
)

async function refresh() {
    try {
        forwards.value = await listForwards(props.sessionId)
    } catch (err) {
        error.value = err instanceof Error ? err.message : String(err)
    }
}

async function add() {
    if (busy.value) return
    if (!bind.value.trim() || (kind.value !== 'dynamic' && !target.value.trim())) {
        error.value = t('fwd.required')
        return
    }
    busy.value = true
    error.value = ''
    try {
        await addForward(props.sessionId, {
            kind: kind.value,
            bind: bind.value.trim(),
            target: kind.value === 'dynamic' ? undefined : target.value.trim(),
        })
        logger.info('fwd', 'forward added session=%s kind=%s', props.sessionId, kind.value)
        bind.value = ''
        target.value = ''
        await refresh()
    } catch (err) {
        error.value = err instanceof Error ? err.message : String(err) || t('fwd.addFailed')
    } finally {
        busy.value = false
    }
}

async function remove(id: string) {
    try {
        await deleteForward(props.sessionId, id)
        await refresh()
    } catch (err) {
        error.value = err instanceof Error ? err.message : String(err)
    }
}

function close() {
    emit('close')
}
</script>

<style scoped>
.fwd-overlay {
    position: fixed;
    inset: 0;
    z-index: 1000;
    display: flex;
    align-items: center;
    justify-content: center;
    background: var(--overlay);
}

.fwd-dialog {
    width: 480px;
    max-width: calc(100vw - 32px);
    background: var(--bg-dialog);
    border: 1px solid var(--border-dialog);
    border-radius: 6px;
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.5);
    display: flex;
    flex-direction: column;
    overflow: hidden;
    max-height: calc(100vh - 48px);
}

.fwd-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 12px 14px;
    border-bottom: 1px solid var(--border-tab);
}

.fwd-title {
    font-size: 14px;
    font-weight: 600;
    line-height: 1.4;
    color: var(--fg-bright);
}

.fwd-close {
    background: none;
    border: none;
    color: var(--fg-dim);
    font-size: 13px;
    line-height: 1;
    padding: 3px 6px;
    border-radius: 3px;
    cursor: pointer;
}

.fwd-close:hover {
    background: var(--bg-tab-hover);
    color: var(--fg-bright);
}

.fwd-body {
    padding: 14px;
    display: flex;
    flex-direction: column;
    gap: 12px;
    overflow-y: auto;
}

.fwd-form {
    display: flex;
    flex-direction: column;
    gap: 8px;
}

.fwd-row {
    display: flex;
    gap: 8px;
}

.fwd-field {
    display: flex;
    flex-direction: column;
    gap: 4px;
    flex: 1 1 0;
    min-width: 0;
}

.fwd-kind-field {
    flex: 0 0 120px;
}

.fwd-label {
    font-size: 12px;
    color: var(--fg-muted);
}

.fwd-input {
    height: 28px;
    padding: 0 8px;
    background: var(--bg-input);
    border: 1px solid var(--border-tab);
    border-radius: 4px;
    color: var(--fg);
    font-size: 12px;
    font-family: inherit;
    outline: none;
    width: 100%;
    min-width: 0;
}

.fwd-input::placeholder {
    color: var(--fg-hint);
}

.fwd-input:focus {
    border-color: var(--accent);
}

.fwd-input:disabled {
    opacity: 0.5;
}

.fwd-add-row {
    display: flex;
    justify-content: flex-end;
}

.btn-primary {
    height: 26px;
    padding: 0 14px;
    background: var(--accent);
    border: none;
    border-radius: 3px;
    color: var(--fg-bright);
    font-size: 12px;
    font-family: inherit;
    cursor: pointer;
}

.btn-primary:hover {
    filter: brightness(1.1);
}

.btn-primary:disabled {
    opacity: 0.6;
    cursor: default;
}

.fwd-error {
    font-size: 12px;
    line-height: 1.5;
    color: var(--net-bad);
    word-break: break-word;
}

.fwd-list-title {
    font-size: 12px;
    color: var(--fg-muted);
    padding-top: 4px;
    border-top: 1px solid var(--border-tab);
}

.fwd-empty {
    font-size: 12px;
    color: var(--fg-hint);
    padding: 8px 0;
}

.fwd-list {
    display: flex;
    flex-direction: column;
    gap: 6px;
}

.fwd-item {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 8px;
    background: var(--bg-tab);
    border: 1px solid var(--border-tab);
    border-radius: 4px;
}

.fwd-item-kind {
    flex: 0 0 auto;
    font-size: 10px;
    line-height: 1;
    padding: 2px 5px;
    border-radius: 3px;
    border: 1px solid var(--border-tab);
    color: var(--fg-muted);
    text-transform: uppercase;
}

.kind-local {
    color: #3fb950;
}

.kind-remote {
    color: #d29922;
}

.kind-dynamic {
    color: #58a6ff;
}

.fwd-item-spec {
    flex: 1 1 auto;
    min-width: 0;
    font-family: 'SF Mono', Consolas, monospace;
    font-size: 12px;
    color: var(--fg);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.fwd-item-del {
    flex: 0 0 auto;
    background: none;
    border: none;
    color: var(--fg-dim);
    font-size: 11px;
    cursor: pointer;
    padding: 2px 5px;
    border-radius: 3px;
}

.fwd-item-del:hover {
    background: var(--bg-tab-hover);
    color: var(--net-bad);
}
</style>